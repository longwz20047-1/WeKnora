# WeKnora API 调试指南

本文档供 AI（Claude Code 等）读取后，能够自主连接服务器、调用 API、查看日志、定位 Bug。

---

## 服务器连接

### SSH 连接

密钥文件已保存在本地 `~/.ssh/bt_key`（由宝塔面板生成的 ED25519 密钥），可直接连接：

```bash
# 直接连接（密钥已就绪）
ssh -o StrictHostKeyChecking=no -i ~/.ssh/bt_key root@192.168.100.30

# 变量简写（后续命令中使用）
SSH="ssh -o StrictHostKeyChecking=no -i ~/.ssh/bt_key root@192.168.100.30"
```

**注意事项**：
- 密钥文件路径：`C:\Users\yaowenlong\.ssh\bt_key`（即 `~/.ssh/bt_key`）
- 密钥必须是 Unix 换行符（LF），Windows 换行符（CRLF）会导致 `error in libcrypto`，修复：`sed -i 's/\r$//' ~/.ssh/bt_key`
- 用户为 `root`（宝塔面板管理），不是 `yaowenlong`

### Docker 容器

| 容器名 | 用途 |
|--------|------|
| `WeKnora-app` | Go 后端主服务 |
| `WeKnora-frontend` | 前端 |
| `WeKnora-postgres` | PostgreSQL 数据库 |
| `WeKnora-redis` | Redis 缓存 |
| `WeKnora-qdrant` | Qdrant 向量数据库 |
| `WeKnora-docreader` | Python 文档解析 gRPC 服务 |

```bash
# 查看运行状态
$SSH "docker ps --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}'"

# 重启某个容器
$SSH "docker restart WeKnora-app"

# 重新构建并启动
$SSH "cd /opt/WeKnora && docker compose up -d --build app"
```

---

## API 认证

### 登录获取 Token

```bash
# 登录端点是 /api/v1/auth/login（注意不是 /api/v1/login）
# Token 在响应的 "token" 字段（不是 "data.access_token"）
TOKEN=$($SSH "curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{\"email\":\"longwz20047@gmail.com\",\"password\":\"ywl20047\"}' \
  | grep -o '\"token\":\"[^\"]*\"' | cut -d'\"' -f4" 2>/dev/null)

echo "Token length: ${#TOKEN}"  # 应该 > 0
```

**登录响应结构**：
```json
{
  "success": true,
  "user": { "id": "...", "email": "...", "tenant_id": 10001, ... },
  "tenant": { "id": 10001, "conversation_config": { ... }, ... },
  "token": "eyJhbGci...",
  "refresh_token": "eyJhbGci..."
}
```

### 认证方式

所有 API 请求都需要在 Header 中携带：
```
Authorization: Bearer <token>
```

---

## 核心 API 端点

### 知识库搜索

```bash
# POST /api/v1/knowledge-search
$SSH "curl -s -X POST http://localhost:8080/api/v1/knowledge-search \
  -H 'Authorization: Bearer $TOKEN' \
  -H 'Content-Type: application/json' \
  -d '{
    \"query\": \"搜索关键词\",
    \"knowledge_base_ids\": [\"kb-id-1\", \"kb-id-2\"],
    \"knowledge_ids\": []
  }'"
```

**请求参数**：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `query` | string | 是 | 搜索查询文本 |
| `knowledge_base_ids` | string[] | 否 | 知识库 ID 列表 |
| `knowledge_base_id` | string | 否 | 单个知识库 ID（兼容旧接口）|
| `knowledge_ids` | string[] | 否 | 指定知识文档 ID |

**响应结构**：
```json
{
  "success": true,
  "data": [
    {
      "id": "chunk-id",
      "content": "文档内容片段...",
      "score": 0.85,
      "match_type": 0,
      "knowledge_id": "...",
      "knowledge_base_id": "..."
    }
  ]
}
```

`match_type` 含义：0=向量匹配, 1=关键词匹配, 2=邻近块

### 其他常用端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/knowledgebases` | 列出知识库 |
| GET | `/api/v1/knowledgebases/:id` | 知识库详情 |
| GET | `/api/v1/knowledgebases/:id/knowledges` | 知识库下的文档 |
| POST | `/api/v1/knowledge-search` | 知识搜索 |
| GET | `/api/v1/knowledgebases/:id/hybrid-search?query=xxx` | 单知识库混合搜索 |
| POST | `/api/v1/chats_stream` | 对话（SSE 流式）|
| GET | `/api/v1/models` | 模型列表 |
| GET | `/health` | 健康检查 |

---

## 日志监控

### 实时日志

```bash
# 实时查看所有日志
$SSH "docker logs -f WeKnora-app 2>&1"

# 实时查看并过滤关键信息
$SSH "docker logs -f WeKnora-app 2>&1 | grep -E '(ERROR|WARN|panic|SearchKnowledge|HybridSearch)'"
```

### 按请求追踪

每个 API 请求都有唯一的 `request_id`，可以用来追踪完整调用链路：

```bash
# 1. 触发请求
$SSH "curl -s -X POST http://localhost:8080/api/v1/knowledge-search \
  -H 'Authorization: Bearer $TOKEN' \
  -H 'Content-Type: application/json' \
  -d '{\"query\":\"docker\",\"knowledge_base_ids\":[\"KB_ID\"]}' > /dev/null"

# 2. 获取最近请求的 request_id
$SSH "docker logs WeKnora-app --since 10s 2>&1 | grep 'SearchKnowledge' | head -1"
# 输出示例: request_id=84366a8c-4593-4a53-9281-2c43998976d6

# 3. 用 request_id 过滤完整链路
$SSH "docker logs WeKnora-app --since 30s 2>&1 | grep '84366a8c'"
```

### 触发请求并立即查日志（一条命令）

```bash
$SSH "curl -s -X POST http://localhost:8080/api/v1/knowledge-search \
  -H 'Authorization: Bearer $TOKEN' \
  -H 'Content-Type: application/json' \
  -d '{\"query\":\"docker\",\"knowledge_base_ids\":[\"KB_ID\"]}' > /dev/null 2>&1 && \
  sleep 1 && \
  docker logs WeKnora-app --since 10s 2>&1 | head -80"
```

### Pipeline 阶段日志

WeKnora 搜索 pipeline 各阶段都有结构化日志，格式为 `[PIPELINE] stage=XXX action=YYY`：

```bash
# 过滤 pipeline 日志（排除冗长的逐条评分）
$SSH "docker logs WeKnora-app --since 30s 2>&1 | \
  sed 's/\x1b\[[0-9;]*m//g' | \
  grep -E 'stage=(Search|Rerank|Merge|FilterTopK).*action=' | \
  grep -v 'input_score' | grep -v 'passage_enrich' | grep -v 'result_score'"
```

**关键 pipeline 日志含义**：

| 日志 | 说明 |
|------|------|
| `stage=Search action=input search_targets=N` | 搜索开始，N 个知识库 |
| `stage=Search action=kb_result hit_count=N kb_id="..."` | 单个知识库搜索完成 |
| `stage=Search action=output result_count=N` | 搜索阶段总输出 |
| `stage=Rerank action=input candidate_cnt=N rerank_model="..."` | Rerank 输入 |
| `stage=Rerank action=model_call passages=N` | 调用 Rerank 模型 API |
| `stage=Rerank action=model_call error="..."` | **Rerank API 报错** |
| `stage=Rerank action=top_score rank=1 score=0.XX` | Rerank 模型打分 |
| `stage=Rerank action=threshold_degrade` | 阈值降级重试 |
| `stage=Rerank action=output filtered_cnt=N` | Rerank 输出（0 = 全部被过滤）|
| `stage=Merge action=output merged_total=N` | 合并阶段输出 |
| `stage=FilterTopK action=output` | 最终输出 |

### 错误日志快速定位

```bash
# 查看最近的错误
$SSH "docker logs WeKnora-app --since 5m 2>&1 | grep -i 'error' | tail -20"

# 查看 panic
$SSH "docker logs WeKnora-app --since 5m 2>&1 | grep -i 'panic' | tail -10"

# 查看 Rerank API 错误（常见问题：passages 数量超限返回 400）
$SSH "docker logs WeKnora-app --since 5m 2>&1 | grep 'Rerank.*error'"
```

---

## 搜索 Pipeline 架构

```
SearchKnowledge (session.go)
  ├─ buildSearchTargets()  →  每个 KB 生成一个 SearchTarget
  ├─ Pipeline: CHUNK_SEARCH → CHUNK_RERANK → CHUNK_MERGE → FILTER_TOP_K
  │
  └─ CHUNK_SEARCH (search.go)
       └─ searchByTargets()
            ├─ goroutine 1 → HybridSearch(KB1)
            │    ├─ embedding API 调用（生成查询向量）
            │    └─ CompositeRetrieveEngine.Retrieve()
            │         ├─ goroutine: VectorRetrieve  → Qdrant gRPC
            │         └─ goroutine: KeywordsRetrieve → Qdrant gRPC
            │
            └─ goroutine 2 → HybridSearch(KB2)
                 ├─ embedding API 调用（生成查询向量）
                 └─ CompositeRetrieveEngine.Retrieve()
                      ├─ goroutine: VectorRetrieve  → Qdrant gRPC
                      └─ goroutine: KeywordsRetrieve → Qdrant gRPC
```

**并发层级**：每个 KB 一个 goroutine，每个 goroutine 内部向量+关键词并发，最多 4 个并发 Qdrant 请求。

**关键阈值**（来自 `config.yaml` 和 tenant `conversation_config`）：

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `vector_threshold` | 0.5 | 向量搜索得分过滤阈值 |
| `keyword_threshold` | 0.3 | 关键词搜索得分过滤阈值 |
| `embedding_top_k` | 10 | 每个知识库取 top K |
| `rerank_threshold` | 0.5 | Rerank 模型打分过滤阈值 |
| `rerank_top_k` | 5 | Rerank 后最终返回条数 |

---

## 常见问题排查

### 问题 1：搜索返回 0 结果

**排查步骤**：

1. 确认搜索阶段是否有结果：
   ```bash
   # 看 stage=Search action=output result_count
   $SSH "docker logs WeKnora-app --since 10s 2>&1 | grep 'stage=Search.*action=output'"
   ```

2. 如果搜索有结果但最终返回 0，检查 Rerank：
   ```bash
   # 看 Rerank 是否报错
   $SSH "docker logs WeKnora-app --since 10s 2>&1 | grep -E 'Rerank.*(error|filtered_cnt=0)'"
   ```

3. 常见原因：
   - **Rerank API 返回 400**：passages 数量超过 API 限制（如 >32 个），需要截断或换 API
   - **Rerank 全部低于阈值**：所有候选的 rerank 得分 < `rerank_threshold`
   - **向量阈值过高**：`vector_threshold` 过高导致向量结果全部被过滤
   - **embedding API 失败**：查看是否有 embedding 相关的 error 日志

### 问题 2：API 返回 401 Unauthorized

- 检查 Token 是否过期
- 确认登录端点是 `/api/v1/auth/login`（不是 `/api/v1/login`）
- 确认 Token 从响应的 `token` 字段获取（不是 `data.access_token`）

### 问题 3：API 返回 404

- 确认路径正确，常见错误：
  - 搜索接口是 `POST /api/v1/knowledge-search`（不是 `/api/v1/knowledgebases/search`）
  - 混合搜索是 `GET /api/v1/knowledgebases/:id/hybrid-search`

### 问题 4：搜索结果质量差

- 检查 embedding 模型是否正常：日志中 `Query embedding generated successfully, embedding vector length: 3072`
- 检查 Rerank 模型得分：日志中 `stage=Rerank action=top_score`
- 中文关键词搜索依赖 Qdrant 的 gojieba 分词，效果有限；如需更好的中文搜索可切换到 Elasticsearch

---

## 测试知识库

| 知识库 | ID | 内容 |
|--------|-----|------|
| claude code | `7ac85a8f-482d-4b95-a99b-8b07cf41eab7` | 1 个文档 |
| 测试知识库 | `2d2ee8a8-703f-418d-88c7-94321b82a866` | 26 个文档 |

```bash
# 获取知识库列表（动态查询）
$SSH "curl -s http://localhost:8080/api/v1/knowledgebases \
  -H 'Authorization: Bearer $TOKEN'" | python -mjson.tool | head -30
```

---

## 配置文件

| 文件 | 容器内路径 | 说明 |
|------|-----------|------|
| `config.yaml` | `/app/config/config.yaml` | 主配置（阈值、prompt 等）|
| `.env` | `/opt/WeKnora/.env` | 环境变量（数据库、模型地址等）|
| `docker-compose.yml` | `/opt/WeKnora/docker-compose.yml` | 容器编排 |

```bash
# 查看容器内 config
$SSH "docker exec WeKnora-app cat /app/config/config.yaml | head -30"

# 查看环境变量
$SSH "cat /opt/WeKnora/.env | grep -v '^#' | grep -v '^$'"

# 注意：config.yaml 默认打包在镜像中，
# 如果 docker-compose.yml 没有挂载 volume，修改本地文件不会生效，需要重新构建镜像
```

---

## 已解决的历史问题

### Rerank API passages 超限（2026-03-02）

**现象**：多知识库搜索返回 0 结果，单知识库正常。

**根因**：Rerank 模型 API（bge-reranker-v2-m3）在接收超过约 32 个 passages 时返回 `400 Bad Request`。多知识库搜索合并后有 47 个 passages 超限。

**日志特征**：
```
ERROR | stage=Rerank action=model_call error="Rerank API error: Http Status: 400 Bad Request" passages=47
WARNING | stage=Rerank action=output filtered_cnt=0
```

**定位方法**：通过 pipeline 日志对比单 KB（23 passages → 成功）和多 KB（47 passages → 400 错误），确认是 passages 数量问题。

**解决方案**：更换支持更多 passages 的 Rerank API。建议额外在代码中加 passages 截断保护。
