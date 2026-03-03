# Elasticsearch 混合搜索方案

## 背景

当前 WeKnora 使用 `RETRIEVE_DRIVER=qdrant` 时，向量搜索和关键词搜索都由 Qdrant 处理。
存在三个问题：

1. **中文关键词搜索失效** — Qdrant 的全文索引 tokenizer 不支持中文分词，中文查询无法正确拆分为词语
2. **标题和摘要不参与搜索** — 当前只有 `chunk.Content` 被索引，`Knowledge.Title` 和 `Knowledge.Description` 未索引，用户按文档标题搜索无法命中
3. **多知识库并发搜索 bug** — gojieba 全局单例并发不安全（已通过 SafeJieba mutex 修复）

本方案引入 Elasticsearch 专门处理关键词搜索，利用 ES 原生的 BM25 评分、中文分词和**多字段加权搜索**能力。

## 方案架构

```
                    ┌──────────────────────────────────────────┐
                    │     CompositeRetrieveEngine               │
                    │                                          │
                    │   retrieveParams[VectorRetrieverType]    │──► Qdrant (gRPC)
                    │                                          │    - HNSW 向量搜索
                    │   retrieveParams[KeywordsRetrieverType]  │──► Elasticsearch
                    │                                          │    - BM25 + IK 中文分词
                    │                                          │    - 多字段搜索 (title^3 + description^2 + content)
                    └──────────────────────────────────────────┘
```

**RETRIEVE_DRIVER=qdrant_es**

- 向量搜索 → Qdrant（保持不变，HNSW 高效索引）
- 关键词搜索 → Elasticsearch（专业全文搜索，原生 CJK 支持，多字段加权）

> **配置说明**：`RETRIEVE_DRIVER` 只需配置一个驱动即可。`qdrant_es` 已包含 Qdrant + ES 两个引擎，
> 不需要也不能与 `elasticsearch_v8` 同时配置（两者都注册 `ElasticsearchRetrieverEngineType`，同时配置会启动失败）。
> 正常用法：`RETRIEVE_DRIVER=qdrant` → `RETRIEVE_DRIVER=qdrant_es`，一行改动即可切换。

### 数据流说明

**搜索时**：`CompositeRetrieveEngine`（`composite.go`）根据 `RetrieverType` 将请求路由到对应引擎：
- `KeywordsRetrieverType` → ES 引擎的 `KeywordsRetrieve()` — 使用 `multi_match` 跨 title/description/content 搜索
- `VectorRetrieverType` → Qdrant 引擎的 `VectorRetrieve()` — 仅基于 content embedding 的向量相似度

**索引时**：`CompositeRetrieveEngine.Index/BatchIndex` 并发调用所有引擎，每个引擎只处理自己负责的 retrieverTypes：
- ES 引擎收到 `retrieverTypes=[keywords]` → `KVHybridRetrieveEngineService` 跳过 embedding 生成，只存文本（content + title + description）
- Qdrant 引擎收到 `retrieverTypes=[vector]` → 生成 embedding 并存储向量

**索引错误处理**：`CompositeRetrieveEngine` 的索引路径（`composite.go`）并发调用各引擎。
如果某个引擎索引失败（如 ES 不可用），当前行为是**整体返回错误**，上层会重试或标记文档处理失败。
这意味着 ES 故障可能阻塞文档索引（即使 Qdrant 成功了）。可接受的理由：
- 索引是低频操作（文档上传时），不影响在线查询
- 部分成功会导致数据不一致（ES 中缺少文档，但 Qdrant 中有），不如整体失败后重试
- 如需更强容错，可考虑后续增加异步重试队列

### 标题/摘要搜索的数据流

**当前（仅搜 content）：**

```
Knowledge.Title ──────── 不索引 ──────── 搜索后才附加到结果（enrichment 阶段）
Knowledge.Description ── 不索引 ──────── 完全不参与搜索
Chunk.Content ────────── 索引到向量库 ── KeywordsRetrieve 只搜 content 字段
```

**改进后（ES 多字段搜索）：**

```
Knowledge.Title ──► IndexInfo.KnowledgeTitle ──► ES "title" 字段 ──► multi_match (boost=3)
Knowledge.Description ► IndexInfo.KnowledgeDesc ► ES "description" 字段 ► multi_match (boost=2)
Chunk.Content ──────► IndexInfo.Content ─────────► ES "content" 字段 ──► multi_match (boost=1)
```

## 代码改动清单

### 1. `internal/types/embedding.go` — IndexInfo 新增标题和摘要字段

```go
type IndexInfo struct {
    ID              string
    Content         string
    SourceID        string
    SourceType      SourceType
    ChunkID         string
    KnowledgeID     string
    KnowledgeBaseID string
    KnowledgeType   string
    TagID           string
    IsEnabled       bool
    IsRecommended   bool
    // 新增：文档级元数据，用于 ES 多字段关键词搜索
    KnowledgeTitle       string // 所属文档标题
    KnowledgeDescription string // 所属文档摘要
}
```

### 2. `internal/types/tenant.go` — 新增混合驱动映射

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

### 3. `internal/container/container.go` — 识别 qdrant_es 驱动

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

### 4. `elasticsearch/structs.go` — VectorEmbedding 结构体改动

需要同时完成四项修改：新增 title/description 字段、新增 TagID 字段、修复 Embedding 序列化、修复 IsEnabled 映射。

#### 4a. 完整的 VectorEmbedding 结构体

```go
type VectorEmbedding struct {
    Content         string    `json:"content"                    gorm:"column:content;not null"`
    SourceID        string    `json:"source_id"                  gorm:"column:source_id;not null"`
    SourceType      int       `json:"source_type"                gorm:"column:source_type;not null"`
    ChunkID         string    `json:"chunk_id"                   gorm:"column:chunk_id"`
    KnowledgeID     string    `json:"knowledge_id"               gorm:"column:knowledge_id"`
    KnowledgeBaseID string    `json:"knowledge_base_id"          gorm:"column:knowledge_base_id"`
    TagID           string    `json:"tag_id,omitempty"           gorm:"column:tag_id"`          // 新增：修复 pre-existing bug
    Embedding       []float32 `json:"embedding,omitempty"        gorm:"column:embedding;not null"` // 改动：omitempty 防止序列化空数组
    IsEnabled       bool      `json:"is_enabled"`
    // 新增：文档级元数据
    Title       string `json:"title,omitempty"`       // 所属文档标题
    Description string `json:"description,omitempty"` // 所属文档摘要
}
```

**修复说明**：

| 字段 | 改动 | 原因 |
|------|------|------|
| `TagID` | **新增** | Pre-existing bug：`getBaseConds()` 过滤 `tag_id` 字段（行 262-268），但 `VectorEmbedding` 从未包含此字段，导致 ES 中 tag_id 始终为空 |
| `Embedding` | json tag 加 `omitempty` | `qdrant_es` 模式下 ES 不存储向量，避免序列化 `"embedding": null` 或 `"embedding": []` 到 ES |
| `Title` | **新增** | 多字段关键词搜索 |
| `Description` | **新增** | 多字段关键词搜索 |

#### 4b. 修改 `ToDBVectorEmbedding` 映射

```go
func ToDBVectorEmbedding(embedding *types.IndexInfo, additionalParams map[string]interface{}) *VectorEmbedding {
    vector := &VectorEmbedding{
        Content:         embedding.Content,
        SourceID:        embedding.SourceID,
        SourceType:      int(embedding.SourceType),
        ChunkID:         embedding.ChunkID,
        KnowledgeID:     embedding.KnowledgeID,
        KnowledgeBaseID: embedding.KnowledgeBaseID,
        TagID:           embedding.TagID,                // 新增：修复 tag_id 映射
        IsEnabled:       embedding.IsEnabled,            // 修复：原来硬编码 true，应取 IndexInfo 的值
        Title:           embedding.KnowledgeTitle,       // 新增
        Description:     embedding.KnowledgeDescription, // 新增
    }
    // Add embedding data if available in additionalParams
    if additionalParams != nil && slices.Contains(slices.Collect(maps.Keys(additionalParams)), "embedding") {
        if embeddingMap, ok := additionalParams["embedding"].(map[string][]float32); ok {
            vector.Embedding = embeddingMap[embedding.SourceID]
        }
    }
    // Get is_enabled from additionalParams if available (覆盖上面的默认值)
    if additionalParams != nil {
        if chunkEnabledMap, ok := additionalParams["chunk_enabled"].(map[string]bool); ok {
            if enabled, exists := chunkEnabledMap[embedding.ChunkID]; exists {
                vector.IsEnabled = enabled
            }
        }
    }
    return vector
}
```

**IsEnabled 修复说明**：原代码硬编码 `IsEnabled: true`，忽略了 `IndexInfo.IsEnabled` 的值。
部分 FAQ 构造点（如 knowledge.go:6304）显式设置 `IsEnabled: chunk.IsEnabled`，硬编码 `true` 会让 disabled 的 chunk 在 ES 中也被标记为 enabled。
修复后优先级：`additionalParams["chunk_enabled"]` > `embedding.IsEnabled`。

### 5. `elasticsearch/v8/repository.go` — 核心改动

#### 5a. 移除 `Save()` 中的空 embedding 强制检查

**问题**：当前 `Save()` 方法在 embedding 为空时直接报错（`repository.go:112-117`）。
在 `qdrant_es` 模式下，ES 只负责关键词搜索，`KVHybridRetrieveEngineService` 不会为 ES 生成 embedding，
导致 `Save()` 必然失败。

**修复**：移除空 embedding 检查，允许不带向量数据的文档写入（关键词搜索只需要文本字段）。

```go
// Save 修改前（repository.go:112-117）:
embeddingDB := elasticsearchRetriever.ToDBVectorEmbedding(embedding, additionalParams)
if len(embeddingDB.Embedding) == 0 {
    err := fmt.Errorf("empty embedding vector for chunk ID: %s", embedding.ChunkID)
    log.Errorf("[Elasticsearch] %v", err)
    return err
}

// Save 修改后:
embeddingDB := elasticsearchRetriever.ToDBVectorEmbedding(embedding, additionalParams)
// 注意：qdrant_es 模式下 ES 只做关键词搜索，embedding 可能为空，这是正常的
// Embedding 字段已加 omitempty，空值不会序列化到 ES
```

#### 5b. `.keyword` 兼容性处理（multi-field mapping 方案）

**问题**：现有代码中 11 处查询使用 `.keyword` 后缀（如 `chunk_id.keyword`），这是因为当前 ES 使用动态 mapping，
`text` 类型字段会自动生成 `.keyword` 子字段。本方案的显式 mapping 将这些字段直接定义为 `"type": "keyword"`，
如果不做处理，不存在 `.keyword` 子字段，**所有过滤/删除/更新操作会静默返回零结果**。

**涉及行数**（共 11 处）：

```
行 178:  chunk_id.keyword        (DeleteByChunkIDList)
行 201:  source_id.keyword       (DeleteBySourceIDList)
行 226:  knowledge_id.keyword    (DeleteByKnowledgeIDList)
行 250:  knowledge_base_id.keyword (getBaseConds)
行 257:  knowledge_id.keyword    (getBaseConds)
行 265:  tag_id.keyword          (getBaseConds)
行 278:  knowledge_id.keyword    (getBaseConds - exclude)
行 283:  chunk_id.keyword        (getBaseConds - exclude)
行 645:  chunk_id.keyword        (BatchUpdateChunkEnabledStatus - enabled)
行 671:  chunk_id.keyword        (BatchUpdateChunkEnabledStatus - disabled)
行 720:  chunk_id.keyword        (BatchUpdateChunkTagID)
```

**修复**（multi-field 方案）：在显式 mapping 中使用 `multi-field` 定义，**同时保留 `field` 和 `field.keyword` 两种访问方式**，
这样无需修改 repository.go 中的 11 处 `.keyword` 引用，对 `elasticsearch_v8` 旧索引（动态 mapping）也完全兼容。

具体做法：在 mapping 中将 `chunk_id`、`source_id` 等字段定义为 `keyword` 类型 + `.keyword` 子字段（指向自身），
或者反过来——保持 `.keyword` 引用不变，在 mapping 中添加 `multi-field`：

```json
"chunk_id": {
    "type": "keyword",
    "fields": {
        "keyword": { "type": "keyword" }
    }
}
```

这样 `chunk_id` 和 `chunk_id.keyword` **都能正确查询**，无论是显式 mapping 还是动态 mapping 创建的索引。

> **之前的方案**（直接去掉 `.keyword` 后缀）风险过高：`repository.go` 中的 11 处 `.keyword` 引用
> 被 `qdrant_es` 和 `elasticsearch_v8` 共享。去掉后缀会导致使用动态 mapping 的旧 `elasticsearch_v8` 索引
> **静默返回零结果**（不报错），比报错更危险。multi-field 方案零改动 repository.go，完全向后兼容。

**代码改动**：`.keyword` 后缀**保持不变**，只需在 `createIndexIfNotExists` 的 mapping 中为每个 keyword 字段添加 `.keyword` 子字段（见下方 5c 节 mapping 设计）。

#### 5c. 修改 `createIndexIfNotExists` 配置中文分析器 + 标题/摘要字段

mapping 包含 `title` 和 `description` 字段，使用与 `content` 相同的 CJK 分析器。
不包含 `embedding`（dense_vector）字段——`qdrant_es` 模式下 ES 只做关键词搜索。
所有 keyword 类型字段使用 multi-field 定义，保留 `.keyword` 子字段以兼容现有 repository.go 代码。

> **重要**：`qdrant_es` 和 `elasticsearch_v8` 应使用不同的索引名称（`ELASTICSEARCH_INDEX`），
> 因为 `qdrant_es` 的 mapping 不含 `embedding` 字段，如果 `elasticsearch_v8` 复用同一索引，
> `VectorRetrieve` 会因缺少 dense_vector 字段而失败。

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

    indexBody := strings.NewReader(`{
        "settings": {
            "number_of_shards": 1,
            "number_of_replicas": 0,
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
                "title": {
                    "type": "text",
                    "analyzer": "content_analyzer",
                    "search_analyzer": "content_search_analyzer"
                },
                "description": {
                    "type": "text",
                    "analyzer": "content_analyzer",
                    "search_analyzer": "content_search_analyzer"
                },
                "content": {
                    "type": "text",
                    "analyzer": "content_analyzer",
                    "search_analyzer": "content_search_analyzer"
                },
                "source_id": {
                    "type": "keyword",
                    "fields": { "keyword": { "type": "keyword" } }
                },
                "source_type": {
                    "type": "integer"
                },
                "chunk_id": {
                    "type": "keyword",
                    "fields": { "keyword": { "type": "keyword" } }
                },
                "knowledge_id": {
                    "type": "keyword",
                    "fields": { "keyword": { "type": "keyword" } }
                },
                "knowledge_base_id": {
                    "type": "keyword",
                    "fields": { "keyword": { "type": "keyword" } }
                },
                "tag_id": {
                    "type": "keyword",
                    "fields": { "keyword": { "type": "keyword" } }
                },
                "is_enabled": {
                    "type": "boolean"
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

#### 5d. 修改 `KeywordsRetrieve` 使用多字段搜索

**修改前**（只搜 content）：

```go
must := []types.Query{
    {Match: map[string]types.MatchQuery{"content": {Query: params.Query}}},
}
```

**修改后**（跨 title/description/content 搜索，标题权重最高）：

```go
must := []types.Query{
    {
        MultiMatch: &types.MultiMatchQuery{
            Query:  params.Query,
            Fields: []string{"title^3", "description^2", "content"},
            Type:   &multimatchBestFields, // "best_fields"
        },
    },
}
```

**字段权重说明：**

| 字段 | Boost | 含义 |
|------|-------|------|
| `title` | ×3 | 标题完全命中说明高度相关 |
| `description` | ×2 | 摘要命中说明文档主题相关 |
| `content` | ×1（默认） | 内容命中是基础匹配 |

**`best_fields` 策略**：取匹配得分最高的字段作为文档最终得分。适合用户搜索单个概念（如 "Docker 部署"），
某个字段完整匹配比多个字段各匹配一部分更有价值。

> **注意**：此改动也会影响 `elasticsearch_v8` 驱动的 `KeywordsRetrieve`。
> 如果 `elasticsearch_v8` 索引中没有 `title`/`description` 字段，`multi_match` 会忽略缺失字段，
> 仍然只搜 `content`，行为与改动前一致。因此这是一个**安全的增强**——有这些字段就搜，没有就跳过。

#### 5e. 修改 `CopyIndices` 保留 title/description

`CopyIndices`（repository.go:468-611）从源索引读取文档并写入目标索引。当前构建 `IndexInfo` 时
（行 571-578）不包含 title/description，需要补充。

```go
// 修改前（repository.go:571-578）:
indexInfo := &typesLocal.IndexInfo{
    Content:         sourceDoc.Content,
    SourceID:        targetSourceID,
    SourceType:      typesLocal.SourceType(sourceDoc.SourceType),
    ChunkID:         targetChunkID,
    KnowledgeID:     targetKnowledgeID,
    KnowledgeBaseID: targetKnowledgeBaseID,
}

// 修改后:
indexInfo := &typesLocal.IndexInfo{
    Content:              sourceDoc.Content,
    SourceID:             targetSourceID,
    SourceType:           typesLocal.SourceType(sourceDoc.SourceType),
    ChunkID:              targetChunkID,
    KnowledgeID:          targetKnowledgeID,
    KnowledgeBaseID:      targetKnowledgeBaseID,
    KnowledgeTitle:       sourceDoc.Title,       // 新增：从源文档保留
    KnowledgeDescription: sourceDoc.Description,  // 新增：从源文档保留
}
```

由于 `sourceDoc` 是从 ES 反序列化的 `VectorEmbedding` 结构体，新增的 `Title`/`Description` 字段
会自动从 ES 源文档中反序列化，无需额外查询。

### 6. 索引构造代码 — 填充标题和摘要

需要修改所有构建 `IndexInfo` 的位置，添加 `KnowledgeTitle` 和 `KnowledgeDescription` 字段。

**完整修改清单**（共 11 处）：

#### 6a. 持有 `knowledge` 对象的构造点（5 处，直接添加字段）

| 文件 | 行号 | 上下文 | knowledge 来源 |
|------|------|--------|----------------|
| `knowledge.go` | 1394 | `processChunks` 文档 chunk 索引 | 函数参数 `knowledge` |
| `knowledge.go` | 1664 | `processMetadataOnlyChunks` 元数据 chunk | 函数参数 `knowledge` |
| `knowledge.go` | 2055 | `indexSummaryChunk` 摘要 chunk | 函数参数 `knowledge` |
| `knowledge.go` | 2222 | `indexGeneratedQuestions` 生成问题 | 函数参数 `knowledge` |
| `extract.go` | 483 | `indexToVectorDB` 数据表 chunk | 函数只接收 `chunks` 切片，不持有 `knowledge` 对象。需要从 chunks 中提取 KnowledgeID 批量查询 |

**`extract.go:483`（indexToVectorDB）处理方式**：

`indexToVectorDB` 的函数签名为 `func (s *DataTableSummaryService) indexToVectorDB(ctx, chunks, engine, embedder)`，
不持有 `knowledge` 对象。建议修改函数签名增加 `knowledgeTitle` 和 `knowledgeDescription` 参数，
由调用方（已持有 knowledge 上下文）传入：

```go
func (s *DataTableSummaryService) indexToVectorDB(
    ctx context.Context,
    chunks []*types.Chunk,
    engine *retriever.CompositeRetrieveEngine,
    embedder embedding.Embedder,
    knowledgeTitle string,       // 新增
    knowledgeDescription string, // 新增
) error {
    indexInfoList := make([]*types.IndexInfo, 0, len(chunks))
    for _, chunk := range chunks {
        indexInfoList = append(indexInfoList, &types.IndexInfo{
            Content:              chunk.Content,
            SourceID:             chunk.ID,
            SourceType:           types.ChunkSourceType,
            ChunkID:              chunk.ID,
            KnowledgeID:          chunk.KnowledgeID,
            KnowledgeBaseID:      chunk.KnowledgeBaseID,
            KnowledgeTitle:       knowledgeTitle,
            KnowledgeDescription: knowledgeDescription,
            IsEnabled:            chunk.IsEnabled,
        })
    }
    // ...
}
```

> **⚠ IsEnabled 零值陷阱**：这 5 个非 FAQ 构造点当前未设置 `IsEnabled` 字段（Go 零值为 `false`）。
> 本方案修复了 `ToDBVectorEmbedding` 中 `IsEnabled` 硬编码 `true` 的 bug，改为从 `IndexInfo.IsEnabled` 取值。
> 如果只添加 title/description 而不同时设置 `IsEnabled`，修复后所有非 FAQ chunk 在 ES 中会被标记为 `disabled`，
> 被 `getBaseConds` 的 `is_enabled` filter 过滤掉，导致**关键词搜索无结果**。
> **因此，添加 title/description 时必须同时设置 `IsEnabled: chunk.IsEnabled`。**

修改示例（以 knowledge.go:1394 为例）：

```go
indexInfoList = append(indexInfoList, &types.IndexInfo{
    Content:              chunk.Content,
    SourceID:             chunk.ID,
    SourceType:           types.ChunkSourceType,
    ChunkID:              chunk.ID,
    KnowledgeID:          knowledge.ID,
    KnowledgeBaseID:      knowledge.KnowledgeBaseID,
    KnowledgeTitle:       knowledge.Title,       // 新增
    KnowledgeDescription: knowledge.Description,  // 新增
    IsEnabled:            chunk.IsEnabled,         // 必须：防止零值导致 chunk 被过滤
})
```

#### 6b. 不持有 `knowledge` 对象的构造点（6 处，需要额外查询）

| 文件 | 行号 | 上下文 | 需要的处理 |
|------|------|--------|-----------|
| `knowledge.go` | 2897 | `updateChunkVector`/`rebuildIndex` | 从 `chunk.KnowledgeID` 批量查询 knowledge 表 |
| `knowledge.go` | 6294 | `buildFAQIndexInfoList` Combined 模式 | 函数签名需增加 `knowledge` 参数或从上层传入 |
| `knowledge.go` | 6323 | `buildFAQIndexInfoList` Separate 标准问 | 同上 |
| `knowledge.go` | 6348 | `buildFAQIndexInfoList` Separate 相似问 | 同上 |
| `knowledge.go` | 6428 | `updateFAQIndexInfo` 标准问更新 | 同上 |
| `knowledge.go` | 6461 | `updateFAQIndexInfo` 相似问更新 | 同上 |

**行 2897（rebuildIndex）处理方式**：

```go
// 在循环前批量查询 knowledge 信息
// 注意：KnowledgeRepository 没有 GetByIDs 方法，使用 GetKnowledgeBatch（需要 tenantID）
knowledgeIDs := make([]string, 0)
for _, chunk := range chunks {
    knowledgeIDs = append(knowledgeIDs, chunk.KnowledgeID)
}
// tenantID 需要从 ctx 或上层参数获取
knowledgeList, err := s.knowledgeRepo.GetKnowledgeBatch(ctx, tenantID, knowledgeIDs)
if err != nil {
    log.Warnf("[updateChunkVector] failed to get knowledge batch: %v, title/description will be empty", err)
}
knowledgeMap := make(map[string]*types.Knowledge)
for _, k := range knowledgeList {
    knowledgeMap[k.ID] = k
}

for _, chunk := range chunks {
    info := &types.IndexInfo{
        Content:         chunk.Content,
        SourceID:        chunk.ID,
        SourceType:      types.ChunkSourceType,
        ChunkID:         chunk.ID,
        KnowledgeID:     chunk.KnowledgeID,
        KnowledgeBaseID: chunk.KnowledgeBaseID,
        IsEnabled:       chunk.IsEnabled,
    }
    if k, ok := knowledgeMap[chunk.KnowledgeID]; ok {
        info.KnowledgeTitle = k.Title
        info.KnowledgeDescription = k.Description
    }
    indexInfo = append(indexInfo, info)
}
```

> **注意**：`updateChunkVector` 的函数签名需要确保能获取 `tenantID`。
> 当前签名 `func (s *knowledgeService) updateChunkVector(ctx, kbID string, chunks)` 没有 `tenantID`，
> 需要从上层调用链传入或从 `ctx` 中提取。

**行 6294/6323/6348（buildFAQIndexInfoList）处理方式**：

建议修改函数签名，增加 `knowledgeTitle`、`knowledgeDescription` 参数，由调用方传入。
FAQ 相关的调用方通常已持有 `knowledge` 对象。

### 分析器说明

| 分析器 | 用途 | Tokenizer | 效果 |
|--------|------|-----------|------|
| `content_analyzer` | 索引时 | `ik_max_word` | 最细粒度分词，"中华人民共和国" → "中华人民共和国/中华人民/中华/华人/人民共和国/人民/共和国/共和/国" |
| `content_search_analyzer` | 搜索时 | `ik_smart` | 智能分词，"中华人民共和国" → "中华人民共和国"，减少噪音 |

> **如果不安装 IK 插件**，可使用 ES 内置的 `smartcn` 分析器作为替代（效果稍差但零配置）。
> `smartcn` 没有 index/search 两种变体，需将两个 analyzer 定义都改为使用 `smartcn` tokenizer：
>
> ```json
> "content_analyzer": {
>     "type": "custom",
>     "tokenizer": "smartcn_tokenizer",
>     "filter": ["lowercase"]
> },
> "content_search_analyzer": {
>     "type": "custom",
>     "tokenizer": "smartcn_tokenizer",
>     "filter": ["lowercase"]
> }
> ```

### 代码改动总结

| 文件 | 改动 | 行数 |
|------|------|------|
| `internal/types/embedding.go` | `IndexInfo` 新增 `KnowledgeTitle`、`KnowledgeDescription` 字段 | +2 行 |
| `internal/types/tenant.go` | 新增 `qdrant_es` mapping 条目 | +4 行 |
| `internal/container/container.go` | `qdrant_es` 触发 Qdrant+ES 初始化 | ~6 行修改 |
| `elasticsearch/structs.go` | `VectorEmbedding` 新增 `Title`/`Description`/`TagID`，`Embedding` 加 omitempty，修复 `IsEnabled` 映射 | ~12 行 |
| `elasticsearch/v8/repository.go` | 移除 `Save()` 空 embedding 检查 | -4 行 |
| `elasticsearch/v8/repository.go` | `.keyword` 后缀**保持不变**（multi-field mapping 兼容） | 0 行 |
| `elasticsearch/v8/repository.go` | `createIndexIfNotExists` 添加 CJK mapping（含 title/description） | ~55 行替换 |
| `elasticsearch/v8/repository.go` | `KeywordsRetrieve` 改为 `multi_match` 多字段搜索 | ~8 行替换 |
| `elasticsearch/v8/repository.go` | `CopyIndices` 保留 title/description | +2 行 |
| `knowledge.go` | 5 处直接添加 title/description + IsEnabled + 6 处需查询/传参 | ~30 行 |
| `extract.go` | 1 处 `indexToVectorDB` 添加 title/description 参数 | ~5 行 |
| **合计** | | **~120 行** |

## 关键代码链路分析

### 为什么这个方案能工作

1. **`retrieverEngineMapping`**（`tenant.go`）定义 `qdrant_es` → Keywords:ES + Vector:Qdrant
2. **`NewCompositeRetrieveEngine`**（`composite.go:65-90`）从 registry 中查找并组装引擎：
   - ES 引擎注册了 `retrieverType=[keywords]`
   - Qdrant 引擎注册了 `retrieverType=[vector]`
3. **搜索路由**（`composite.go:34-62`）：遍历 `engineInfos`，用 `slices.Contains(engineInfo.retrieverType, param.RetrieverType)` 找到匹配的引擎
4. **索引写入**（`keywords_vector_hybrid_indexer.go:45-58`）：`Index()` 方法检查 `retrieverTypes` 是否包含 `VectorRetrieverType`，只在需要时才生成 embedding
5. **标题/摘要传递链**：`IndexInfo.KnowledgeTitle/Description` → ES `ToDBVectorEmbedding` → ES 文档 `title`/`description` 字段 → `multi_match` 搜索

### 标题/摘要搜索为什么不影响向量搜索

- 向量搜索只基于 `Content` 字段的 embedding，`KnowledgeTitle`/`KnowledgeDescription` 不会被 embedding
- `KVHybridRetrieveEngineService.Index()` 生成 embedding 时只取 `Content` 字段
- Qdrant 存储时 `toQdrantVectorEmbedding` 不映射 title/description（它们是 `IndexInfo` 的新字段，Qdrant 映射函数不使用）
- 这些字段**只在 ES 关键词搜索路径**中发挥作用

### 需要注意的兼容性

- **单选配置**：`RETRIEVE_DRIVER` 只填一个驱动，`qdrant_es` 已包含两个引擎，不需要也不能与 `elasticsearch_v8` 叠加
- **索引隔离**：`qdrant_es` 和 `elasticsearch_v8` 应使用不同的 `ELASTICSEARCH_INDEX`，因为 mapping 不同（qdrant_es 无 embedding 字段）
- **`.keyword` 兼容**：mapping 使用 multi-field 定义，`field.keyword` 引用在显式 mapping 和动态 mapping 中均可正常工作，无需修改 repository.go 代码
- **`multi_match` 增强**：对 `elasticsearch_v8` 用户也生效，但无 title/description 字段时 ES 会自动忽略缺失字段，行为与改动前一致
- `qdrant_es` 模式下，ES 只被分配 `keywords` 类型，但 `Support()` 仍返回 `[keywords, vector]`，不影响功能——`NewCompositeRetrieveEngine` 只用 mapping 中指定的类型
- `IndexInfo` 新增字段是可选的（零值为空字符串），不影响现有 Qdrant/Postgres 驱动
- ES `title`/`description` 使用 `omitempty`，旧数据（无 title/description）的 `multi_match` 仍然能正常搜索 content 字段

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

# Elasticsearch 配置（注意：qdrant_es 应使用独立索引名，不要与 elasticsearch_v8 共用）
ELASTICSEARCH_ADDR=http://elasticsearch:9200
ELASTICSEARCH_INDEX=weknora_keywords

# 如果 ES 开启了安全认证
# ELASTICSEARCH_USERNAME=elastic
# ELASTICSEARCH_PASSWORD=your_password

# Qdrant 配置保持不变
QDRANT_HOST=qdrant
QDRANT_PORT=6334
QDRANT_COLLECTION=weknora_embeddings
```

### 3. 重新索引已有文档

切换到 `qdrant_es` 后，ES 中还没有已有文档的数据。需要触发重新索引。

**注意**：重新索引必须在代码改动（添加 title/description 到 IndexInfo）部署后执行，
否则 ES 中的 title/description 字段仍为空。

**方式 A — 通过 API 重新上传文档**（简单但慢）：
对每个知识库中的每个文档，重新执行一次"解析并索引"操作。

**方式 B — 编写迁移脚本**（推荐）：
从 PostgreSQL 的 `chunks` 表和 `knowledges` 表联合查询，写入 ES。
需要 `content`、`title`（from knowledge）、`description`（from knowledge）、`chunk_id`、`knowledge_id`、
`knowledge_base_id`、`source_id`、`source_type`、`is_enabled`、`tag_id` 字段。

```sql
-- 迁移数据源查询
SELECT c.id AS chunk_id, c.content, c.knowledge_id, c.knowledge_base_id,
       c.is_enabled, c.tag_id, c.source_type,
       k.title AS knowledge_title, k.description AS knowledge_description
FROM chunks c
JOIN knowledges k ON c.knowledge_id = k.id
WHERE c.deleted_at IS NULL;
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

### 3. 验证多字段中文搜索

```bash
# 索引一条测试文档（含 title 和 description）
curl -X POST "http://localhost:9200/weknora_keywords/_doc" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Docker 容器化部署指南",
    "description": "本文介绍如何使用 Docker 部署微服务架构应用",
    "content": "第一步：安装 Docker Engine。执行 apt-get install docker-ce...",
    "knowledge_base_id": "test-kb",
    "chunk_id": "test-chunk-1",
    "is_enabled": true
  }'

# 按标题搜索（内容中没有"指南"二字，但标题有）
curl -X POST "http://localhost:9200/weknora_keywords/_search?pretty" \
  -H "Content-Type: application/json" \
  -d '{
    "query": {
      "multi_match": {
        "query": "部署指南",
        "fields": ["title^3", "description^2", "content"],
        "type": "best_fields"
      }
    }
  }'
# 期望: 返回测试文档，标题匹配贡献最高分

# 按摘要关键词搜索
curl -X POST "http://localhost:9200/weknora_keywords/_search?pretty" \
  -H "Content-Type: application/json" \
  -d '{
    "query": {
      "multi_match": {
        "query": "微服务架构",
        "fields": ["title^3", "description^2", "content"],
        "type": "best_fields"
      }
    }
  }'
# 期望: 返回测试文档，description 字段命中
```

### 4. 验证 WeKnora API 端到端

```bash
SSH="ssh -o StrictHostKeyChecking=no -i ~/.ssh/bt_key root@192.168.100.30"
TOKEN=$($SSH "curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{\"email\":\"longwz20047@gmail.com\",\"password\":\"ywl20047\"}' \
  | grep -o '\"token\":\"[^\"]*\"' | cut -d'\"' -f4" 2>/dev/null)

# 搜索文档标题关键词
$SSH "curl -s -X POST http://localhost:8080/api/v1/knowledge-search \
  -H 'Authorization: Bearer $TOKEN' \
  -H 'Content-Type: application/json' \
  -d '{\"query\":\"部署指南\",\"knowledge_base_ids\":[\"2d2ee8a8-703f-418d-88c7-94321b82a866\"]}'"
# 期望: 关键词搜索结果中包含 match_type=1（关键词匹配），标题含"部署指南"的文档排在前面
```

## 回退方案

如需回退到纯 Qdrant 方案：

1. 将 `RETRIEVE_DRIVER=qdrant_es` 改回 `RETRIEVE_DRIVER=qdrant`
2. 重启 WeKnora 服务
3. ES 服务可保留或停止，不影响功能

代码改动是向后兼容的——`qdrant` 驱动的行为完全不变。`IndexInfo` 的新字段在 Qdrant 路径中被忽略。

## 风险评估

| 风险 | 等级 | 缓解措施 |
|------|------|----------|
| ES 服务不可用导致关键词搜索失败 | 中 | 向量搜索仍可用；可快速回退到 qdrant |
| IK 插件版本与 ES 版本不兼容 | 低 | 使用 smartcn 内置插件作为备选 |
| 重新索引耗时较长 | 低 | 可后台执行，不影响现有服务 |
| ES 内存占用 | 低 | 单节点 512MB 足够小规模使用 |
| `Save()` 空 embedding 导致索引失败 | 已修复 | 移除空 embedding 检查 + Embedding 字段 omitempty |
| `.keyword` 兼容 | 已解决 | multi-field mapping 同时支持 `field` 和 `field.keyword`，无需修改 repository.go |
| 旧 `elasticsearch_v8` 索引不兼容 | 无 | multi-field 方案对动态 mapping 和显式 mapping 均兼容 |
| 旧数据无 title/description | 低 | `multi_match` 忽略缺失字段，fallback 到仅搜 content；重新索引后自动修复 |
| `IndexInfo` 新字段影响其他驱动 | 无 | 零值空字符串，Qdrant/Postgres 映射函数不使用这些字段 |
| `qdrant_es` 与 `elasticsearch_v8` 叠加配置 | 已规避 | 单选驱动，`qdrant_es` 已包含两个引擎；误配会因 engine type 重复注册启动失败（自动拦截） |
| `CopyIndices` 丢失 title/description | 已修复 | 从 ES 源文档反序列化新字段并传递 |
| TagID 过滤无效（pre-existing） | 已修复 | `VectorEmbedding` 新增 TagID 字段 + 映射 |
| IsEnabled 硬编码 true（pre-existing） | 已修复 | `ToDBVectorEmbedding` 改为从 `IndexInfo.IsEnabled` 取值 |
