# Elasticsearch 混合搜索方案

## 背景

当前 WeKnora 使用 `RETRIEVE_DRIVER=qdrant` 时，向量搜索和关键词搜索都由 Qdrant 处理。
存在两个问题：

1. **中文关键词搜索失效** — Qdrant 的全文索引 tokenizer 不支持中文分词，中文查询无法正确拆分为词语
2. **多知识库并发搜索 bug** — gojieba 全局单例并发不安全（已通过 SafeJieba mutex 修复）

本方案引入 Elasticsearch 专门处理关键词搜索，利用 ES 原生的 BM25 评分和中文分词能力。

## 方案架构

```
                    ┌─────────────────────────────────────┐
                    │     CompositeRetrieveEngine          │
                    │                                     │
                    │   retrieveParams[VectorRetrieverType]│──► Qdrant (gRPC)
                    │                                     │    - HNSW 向量搜索
                    │   retrieveParams[KeywordsRetrieverType]──► Elasticsearch
                    │                                     │    - BM25 + IK 中文分词
                    └─────────────────────────────────────┘
```

**RETRIEVE_DRIVER=qdrant_es**

- 向量搜索 → Qdrant（保持不变，HNSW 高效索引）
- 关键词搜索 → Elasticsearch（专业全文搜索，原生 CJK 支持）

## 代码改动清单

### 1. `internal/types/tenant.go` — 新增混合驱动映射

在 `retrieverEngineMapping` 中添加 `qdrant_es` 条目：

```go
var retrieverEngineMapping = map[string][]RetrieverEngineParams{
    // ... 已有条目 ...
    "qdrant": {
        {RetrieverType: KeywordsRetrieverType, RetrieverEngineType: QdrantRetrieverEngineType},
        {RetrieverType: VectorRetrieverType, RetrieverEngineType: QdrantRetrieverEngineType},
    },
    // 新增：Qdrant 向量 + Elasticsearch 关键词
    "qdrant_es": {
        {RetrieverType: KeywordsRetrieverType, RetrieverEngineType: ElasticsearchRetrieverEngineType},
        {RetrieverType: VectorRetrieverType, RetrieverEngineType: QdrantRetrieverEngineType},
    },
}
```

### 2. `internal/container/container.go` — 识别 qdrant_es 驱动

当前 container.go 通过 `slices.Contains(retrieveDriver, "qdrant")` 和 `slices.Contains(retrieveDriver, "elasticsearch_v8")` 分别判断是否初始化对应客户端。

需要让 `qdrant_es` 同时触发两者的初始化：

```go
// 在 initRetrieveEngineRegistry 函数中，替换原来的条件判断

// Qdrant 初始化（qdrant 或 qdrant_es 都需要）
needsQdrant := slices.Contains(retrieveDriver, "qdrant") || slices.Contains(retrieveDriver, "qdrant_es")
if needsQdrant {
    // ... 现有 Qdrant 初始化代码 ...
}

// ES v8 初始化（elasticsearch_v8 或 qdrant_es 都需要）
needsES := slices.Contains(retrieveDriver, "elasticsearch_v8") || slices.Contains(retrieveDriver, "qdrant_es")
if needsES {
    // ... 现有 ES v8 初始化代码 ...
}
```

### 3. `elasticsearch/v8/repository.go` — 配置中文分析器

修改 `createIndexIfNotExists` 方法，在创建索引时配置 IK 中文分析器：

```go
func (e *elasticsearchRepository) createIndexIfNotExists(ctx context.Context) error {
    log := logger.GetLogger(ctx)

    exists, err := e.client.Indices.Exists(e.index).Do(ctx)
    if err != nil {
        return err
    }
    if exists {
        return nil
    }

    log.Infof("[Elasticsearch] Creating index with CJK analyzer: %s", e.index)

    // 创建索引（使用 raw JSON body 配置 mapping + settings）
    indexBody := strings.NewReader(`{
        "settings": {
            "analysis": {
                "analyzer": {
                    "content_analyzer": {
                        "type": "custom",
                        "tokenizer": "ik_max_word",
                        "filter": ["lowercase"]
                    },
                    "content_search_analyzer": {
                        "type": "custom",
                        "tokenizer": "ik_smart",
                        "filter": ["lowercase"]
                    }
                }
            }
        },
        "mappings": {
            "properties": {
                "content": {
                    "type": "text",
                    "analyzer": "content_analyzer",
                    "search_analyzer": "content_search_analyzer"
                },
                "source_id": {
                    "type": "keyword"
                },
                "source_type": {
                    "type": "integer"
                },
                "chunk_id": {
                    "type": "keyword"
                },
                "knowledge_id": {
                    "type": "keyword"
                },
                "knowledge_base_id": {
                    "type": "keyword"
                },
                "tag_id": {
                    "type": "keyword"
                },
                "is_enabled": {
                    "type": "boolean"
                },
                "embedding": {
                    "type": "dense_vector",
                    "dims": 3072,
                    "index": true,
                    "similarity": "cosine"
                }
            }
        }
    }`)

    _, err = e.client.Indices.Create(e.index).Raw(indexBody).Do(ctx)
    if err != nil {
        log.Errorf("[Elasticsearch] Failed to create index: %v", err)
        return err
    }

    log.Infof("[Elasticsearch] Index created successfully: %s", e.index)
    return nil
}
```

**分析器说明：**

| 分析器 | 用途 | Tokenizer | 效果 |
|--------|------|-----------|------|
| `content_analyzer` | 索引时 | `ik_max_word` | 最细粒度分词，"中华人民共和国" → "中华人民共和国/中华人民/中华/华人/人民共和国/人民/共和国/共和/国" |
| `content_search_analyzer` | 搜索时 | `ik_smart` | 智能分词，"中华人民共和国" → "中华人民共和国"，减少噪音 |

> **如果不安装 IK 插件**，可使用 ES 内置的 `smartcn` 分析器作为替代（效果稍差但零配置）：
> 将 `ik_max_word` 替换为 `smartcn`，`ik_smart` 也替换为 `smartcn`。

### 代码改动总结

| 文件 | 改动 | 行数 |
|------|------|------|
| `internal/types/tenant.go` | 新增 `qdrant_es` mapping 条目 | +4 行 |
| `internal/container/container.go` | `qdrant_es` 触发 Qdrant+ES 初始化 | ~6 行修改 |
| `elasticsearch/v8/repository.go` | `createIndexIfNotExists` 添加中文分析器 mapping | ~50 行替换 |
| **合计** | | **~60 行** |

## 基础设施改动

### 1. 部署 Elasticsearch + IK 分词插件

在 `docker-compose.yml` 中添加 ES 服务：

```yaml
  elasticsearch:
    # 使用预装 IK 插件的镜像，或者官方镜像+启动时安装
    image: elasticsearch:8.17.0
    container_name: WeKnora-elasticsearch
    environment:
      - discovery.type=single-node
      - xpack.security.enabled=false
      - ES_JAVA_OPTS=-Xms512m -Xmx512m
    ports:
      - "${ELASTICSEARCH_PORT:-9200}:9200"
    volumes:
      - elasticsearch_data:/usr/share/elasticsearch/data
    networks:
      - WeKnora-network
    restart: unless-stopped
    # 启动后安装 IK 插件（首次启动需要）
    # 或者构建自定义镜像预装 IK 插件
    profiles:
      - elasticsearch
      - full

volumes:
  elasticsearch_data:
```

**安装 IK 插件的两种方式：**

方式 A — 构建自定义镜像（推荐生产环境）：

```dockerfile
FROM elasticsearch:8.17.0
RUN elasticsearch-plugin install analysis-ik
```

方式 B — 容器启动后手动安装：

```bash
docker exec WeKnora-elasticsearch elasticsearch-plugin install analysis-ik
docker restart WeKnora-elasticsearch
```

方式 C — 不安装 IK，使用内置 smartcn（最简单）：

```bash
docker exec WeKnora-elasticsearch elasticsearch-plugin install analysis-smartcn
docker restart WeKnora-elasticsearch
```

### 2. 环境变量配置

在 `.env` 文件中：

```bash
# 将 RETRIEVE_DRIVER 从 qdrant 改为 qdrant_es
RETRIEVE_DRIVER=qdrant_es

# Elasticsearch 配置
ELASTICSEARCH_ADDR=http://elasticsearch:9200
ELASTICSEARCH_INDEX=weknora_embeddings

# 如果 ES 开启了安全认证
# ELASTICSEARCH_USERNAME=elastic
# ELASTICSEARCH_PASSWORD=your_password

# Qdrant 配置保持不变
QDRANT_HOST=qdrant
QDRANT_PORT=6334
QDRANT_COLLECTION=weknora_embeddings
```

### 3. 重新索引已有文档

切换到 `qdrant_es` 后，ES 中还没有已有文档的数据。需要触发重新索引：

**方式 A — 通过 API 重新上传文档**（简单但慢）：
对每个知识库中的每个文档，重新执行一次"解析并索引"操作。

**方式 B — 编写迁移脚本**（推荐）：
从 Qdrant 中读取所有现有文档，写入 ES。只需要 `content`、`chunk_id`、`knowledge_id`、
`knowledge_base_id`、`source_id`、`source_type`、`is_enabled`、`tag_id` 字段，
不需要 embedding 向量（关键词搜索不需要向量）。

```bash
# 伪代码流程
1. 从 Qdrant 的 weknora_embeddings_3072 集合滚动读取所有 points
2. 提取 payload 字段
3. 批量写入 ES 的 weknora_embeddings 索引
```

**方式 C — 知识库级别重建索引**（推荐）：
WeKnora 可能已有重建索引的 API 或命令，利用现有机制逐个知识库重建。

## 验证步骤

### 1. 验证 ES 服务健康

```bash
curl http://localhost:9200/_cluster/health?pretty
# 期望: status = "green" 或 "yellow"
```

### 2. 验证 IK 分词器

```bash
curl -X POST "http://localhost:9200/_analyze?pretty" \
  -H "Content-Type: application/json" \
  -d '{"analyzer": "ik_smart", "text": "Docker容器化部署最佳实践"}'

# 期望输出 tokens: ["docker", "容器", "化", "部署", "最佳", "实践"]
```

### 3. 验证中文关键词搜索

```bash
# 索引一条测试文档
curl -X POST "http://localhost:9200/weknora_embeddings/_doc" \
  -H "Content-Type: application/json" \
  -d '{
    "content": "Docker容器化部署是现代微服务架构的基础",
    "knowledge_base_id": "test-kb",
    "chunk_id": "test-chunk-1",
    "is_enabled": true
  }'

# 搜索中文关键词
curl -X POST "http://localhost:9200/weknora_embeddings/_search?pretty" \
  -H "Content-Type: application/json" \
  -d '{
    "query": {
      "match": {"content": "容器部署"}
    }
  }'
# 期望: 返回测试文档，score > 0
```

### 4. 验证多知识库搜索

通过 WeKnora API 测试之前失败的场景：

```bash
curl -X POST http://<server>:8080/api/v1/knowledgebases/search \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "knowledge_base_ids": ["<kb1>", "<kb2>"],
    "query": "docker"
  }'
# 期望: 返回两个 KB 的合集结果
```

## 回退方案

如需回退到纯 Qdrant 方案：

1. 将 `RETRIEVE_DRIVER=qdrant_es` 改回 `RETRIEVE_DRIVER=qdrant`
2. 重启 WeKnora 服务
3. ES 服务可保留或停止，不影响功能

代码改动是向后兼容的——`qdrant` 驱动的行为完全不变。

## 风险评估

| 风险 | 等级 | 缓解措施 |
|------|------|----------|
| ES 服务不可用导致关键词搜索失败 | 中 | 向量搜索仍可用；可快速回退到 qdrant |
| IK 插件版本与 ES 版本不兼容 | 低 | 使用 smartcn 内置插件作为备选 |
| 重新索引耗时较长 | 低 | 可后台执行，不影响现有服务 |
| ES 内存占用 | 低 | 单节点 512MB 足够小规模使用 |
