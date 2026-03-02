# 多知识库搜索 Bug 分析报告

## 问题概述

**现象**：通过 `POST /api/v1/knowledge-search` 接口同时搜索 **两个不同的真实知识库** 时，返回 0 条结果。但单独搜索每个知识库均能正常返回结果。

**影响范围**：
- 多知识库关键词搜索
- 多知识库混合搜索（向量 + 关键词）
- 前端跨知识库搜索 & 对话问答场景

**环境**：
- 后端：WeKnora Go 服务
- 向量引擎：`RETRIEVE_DRIVER=qdrant`（Qdrant v1.16.2，gRPC 连接）
- 部署方式：Docker Compose / K8S

---

## 测试环境

| 项目 | 值 |
|------|-----|
| 服务地址 | `192.168.100.30:8080`（Docker）|
| 登录账号 | `longwz20047@gmail.com` / `ywl20047` |
| KB1 | `7ac85a8f-482d-4b95-a99b-8b07cf41eab7`（claude code 知识库，1 个文档）|
| KB2 | `2d2ee8a8-703f-418d-88c7-94321b82a866`（测试知识库，26 个文档）|
| 测试查询词 | `docker` |

---

## 测试方法

### 1. 获取认证 Token

```bash
TOKEN=$(curl -s -X POST http://192.168.100.30:8080/api/v1/login \
  -H "Content-Type: application/json" \
  -d '{"email":"longwz20047@gmail.com","password":"ywl20047"}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['access_token'])")

echo "Token: $TOKEN"
```

### 2. 单知识库搜索（对照组）

```bash
# 搜索 KB1（期望返回 > 0 条结果）
curl -s -X POST http://192.168.100.30:8080/api/v1/knowledge-search \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "docker",
    "knowledge_base_ids": ["7ac85a8f-482d-4b95-a99b-8b07cf41eab7"]
  }' | python3 -c "import sys,json; d=json.load(sys.stdin)['data']; print(f'KB1 results: {len(d) if isinstance(d,list) else 0}')"

# 搜索 KB2（期望返回 > 0 条结果）
curl -s -X POST http://192.168.100.30:8080/api/v1/knowledge-search \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "docker",
    "knowledge_base_ids": ["2d2ee8a8-703f-418d-88c7-94321b82a866"]
  }' | python3 -c "import sys,json; d=json.load(sys.stdin)['data']; print(f'KB2 results: {len(d) if isinstance(d,list) else 0}')"
```

### 3. 多知识库搜索（Bug 复现）

```bash
# 同时搜索 KB1 + KB2（BUG：返回 0 条结果）
curl -s -X POST http://192.168.100.30:8080/api/v1/knowledge-search \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "docker",
    "knowledge_base_ids": ["7ac85a8f-482d-4b95-a99b-8b07cf41eab7","2d2ee8a8-703f-418d-88c7-94321b82a866"]
  }' | python3 -c "import sys,json; d=json.load(sys.stdin)['data']; print(f'BOTH results: {len(d) if isinstance(d,list) else 0}')"
```

### 4. 交叉对照测试

验证 bug 的触发条件：

```bash
# 测试 A：同一 KB 重复两次（期望：正常返回）
curl -s -X POST http://192.168.100.30:8080/api/v1/knowledge-search \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "docker",
    "knowledge_base_ids": ["7ac85a8f-482d-4b95-a99b-8b07cf41eab7","7ac85a8f-482d-4b95-a99b-8b07cf41eab7"]
  }' | python3 -c "import sys,json; d=json.load(sys.stdin)['data']; print(f'KB1 x2: {len(d) if isinstance(d,list) else 0}')"

# 测试 B：不存在的 KB + 真实 KB（期望：正常返回真实 KB 的结果）
curl -s -X POST http://192.168.100.30:8080/api/v1/knowledge-search \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "docker",
    "knowledge_base_ids": ["00000000-0000-0000-0000-000000000000","7ac85a8f-482d-4b95-a99b-8b07cf41eab7"]
  }' | python3 -c "import sys,json; d=json.load(sys.stdin)['data']; print(f'fake+KB1: {len(d) if isinstance(d,list) else 0}')"

# 测试 C：调换 KB 顺序（验证顺序是否影响）
curl -s -X POST http://192.168.100.30:8080/api/v1/knowledge-search \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "docker",
    "knowledge_base_ids": ["2d2ee8a8-703f-418d-88c7-94321b82a866","7ac85a8f-482d-4b95-a99b-8b07cf41eab7"]
  }' | python3 -c "import sys,json; d=json.load(sys.stdin)['data']; print(f'KB2+KB1: {len(d) if isinstance(d,list) else 0}')"
```

### 5. 耗时分析测试

```bash
# 对比成功和失败场景的响应时间
for desc cmd in \
  "KB1_alone" '{"query":"docker","knowledge_base_ids":["7ac85a8f-482d-4b95-a99b-8b07cf41eab7"]}' \
  "KB2_alone" '{"query":"docker","knowledge_base_ids":["2d2ee8a8-703f-418d-88c7-94321b82a866"]}' \
  "BOTH_KBs"  '{"query":"docker","knowledge_base_ids":["7ac85a8f-482d-4b95-a99b-8b07cf41eab7","2d2ee8a8-703f-418d-88c7-94321b82a866"]}' \
  "KB1_x2"    '{"query":"docker","knowledge_base_ids":["7ac85a8f-482d-4b95-a99b-8b07cf41eab7","7ac85a8f-482d-4b95-a99b-8b07cf41eab7"]}'; do
  echo -n "$desc: "
  start=$(date +%s%N)
  curl -s -X POST http://192.168.100.30:8080/api/v1/knowledge-search \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "$cmd" > /dev/null
  end=$(date +%s%N)
  echo "$(( (end - start) / 1000000 ))ms"
done
```

### 6. 服务端日志检查

```bash
# 在触发多 KB 搜索的前后查看日志
ssh yaowenlong@192.168.100.30

# 实时跟踪日志
docker logs -f WeKnora-app 2>&1 | grep -E "(Tokenized|HybridSearch|SearchKnowledge|concurrentRetrieve|error|Error|panic)" &

# 触发搜索后观察上面的日志输出
# 关注：
# 1. 是否有 error/panic 信息
# 2. Tokenized 行是否出现（若不出现说明搜索未到达 Qdrant 阶段）
# 3. HybridSearch 是否被调用了 2 次（每个 KB 一次）
# 4. concurrentRetrieve 的 partial results 警告
```

---

## 已观测到的测试结果

### 结果汇总表

| 测试场景 | 修复前结果 | 修复后结果 | 状态 |
|----------|-----------|-----------|------|
| Docker KB1 单独 | 5 条 | 4 条 | 正常（数量变化因 vector_threshold 调整）|
| Docker KB2 单独 | 3 条 | 1 条 | 正常 |
| Docker KB1 + KB2 | **0 条** | **0 条** | **BUG 未修复** |
| Nginx 代理 KB1 + KB2 | **0 条** | **0 条** | **BUG 未修复** |
| K8S KB1 + KB2 | 4 条 | **0 条** | **BUG 恶化**（vector_threshold 0.5→0.3 引起）|
| K8S KB2 单独 | 4 条 | 4 条 | 正常 |
| Docker KB1 重复 x2 | 5 条 | 4 条 | 正常（同一 KB 重复不触发 bug）|
| Docker KB2 重复 x2 | — | 1 条 | 正常 |
| Docker fake + KB1 | 5 条 | 4 条 | 正常（不存在的 KB + 真实 KB 不触发 bug）|
| Docker fake + KB2 | — | 1 条 | 正常 |

### Bug 触发规律

**100% 可复现**，以下条件必须同时满足：
1. `knowledge_base_ids` 中包含 **两个不同的真实知识库**
2. 两个知识库中都有可匹配的文档

不触发的情况：
- 单个知识库搜索 → 正常
- 同一个知识库 ID 重复两次 → 正常
- 不存在的 KB + 真实 KB → 正常（返回真实 KB 的结果）
- 搜索顺序无关（KB1+KB2 与 KB2+KB1 结果相同）

### 关键耗时线索

| 测试场景 | 响应时间 | 分析 |
|----------|---------|------|
| Docker KB1 单独 | 2.340s | 正常（包含 embedding 调用 ~1.2s）|
| Docker KB2 单独 | 4.314s | 正常（KB2 文档多，耗时更长）|
| **Docker KB1 + KB2** | **1.321s** | **异常短！比单个 KB 还快** |
| Docker KB1 x2 | 5.813s | 正常（两次搜索并发）|
| Docker fake + KB1 | 6.663s | 正常 |

**关键发现**：失败的多 KB 搜索（1.321s）比单次 embedding API 调用（~1.2-2s）还短，说明 **bug 发生在 embedding/搜索执行之前或刚开始时**，而非在 Qdrant 搜索阶段。

---

## 已排除的假设

### 假设 1：gojieba 全局单例并发不安全

**理由**：Qdrant 的 `KeywordsRetrieve` 使用 gojieba 做中文分词，多个 goroutine 并发调用可能导致崩溃或返回空结果。

**修复措施**：在 `internal/types/evaluation.go` 中添加 `SafeJieba` 互斥锁封装。

**结论**：❌ 已排除。部署 SafeJieba 后 bug 完全没有改善。

### 假设 2：concurrentRetrieve 错误处理过于激进

**理由**：`concurrentRetrieve` 中任何一个 goroutine 失败会丢弃所有结果。

**修复措施**：修改为收集部分结果，仅在全部失败时才返回错误。

**结论**：❌ 已排除。部署后 bug 没有改善。

### 假设 3：vector_threshold 过高

**理由**：向量搜索得分（0.35-0.47）低于阈值（0.5），导致向量结果全部被过滤。

**修复措施**：将 `config.yaml` 中 `vector_threshold` 从 0.5 调低至 0.3。

**结论**：⚠️ 部分有效但有副作用。单 KB 搜索确实能返回更多向量结果，但反而导致 K8S 多 KB 搜索从返回 4 条恶化为 0 条。

---

## 代码调用链路

```
SearchKnowledge (session.go:1099)
  ├─ buildSearchTargets()  →  每个 KB 生成一个 SearchTarget
  ├─ Pipeline: CHUNK_SEARCH → CHUNK_RERANK → CHUNK_MERGE → FILTER_TOP_K
  │
  └─ CHUNK_SEARCH (search.go:60)
       └─ searchByTargets (search.go:314)
            ├─ goroutine 1 → HybridSearch(KB1)
            │    └─ CompositeRetrieveEngine.Retrieve()
            │         ├─ goroutine: VectorRetrieve(KB1)  → Qdrant gRPC
            │         └─ goroutine: KeywordsRetrieve(KB1) → Qdrant gRPC
            │
            └─ goroutine 2 → HybridSearch(KB2)
                 └─ CompositeRetrieveEngine.Retrieve()
                      ├─ goroutine: VectorRetrieve(KB2)  → Qdrant gRPC
                      └─ goroutine: KeywordsRetrieve(KB2) → Qdrant gRPC
```

**并发层级**：最多 4 个并发 goroutine 同时执行搜索操作，共享同一个 Qdrant gRPC 连接。

---

## 待排查方向

### 方向 1：Embedding API 并发问题（优先级高）

两个 `HybridSearch` goroutine 分别独立调用 embedding API 生成查询向量。如果 Go HTTP client 在并发调用同一 embedding endpoint 时存在问题（连接复用、响应混淆、超时等），可能导致其中一个或两个 goroutine 拿到无效向量。

**验证方法**：在 `HybridSearch` 中添加日志，记录 embedding 调用前后的时间、返回的向量维度和前几个分量值。

### 方向 2：Context 共享导致的竞态条件（优先级高）

`searchByTargets` 中两个 goroutine 共享来自 `logger.CloneContext(ctx)` 的 context。如果 context 中携带了可变状态（如 tracing span、logger fields），并发写入可能导致 data race。

**验证方法**：使用 `go test -race` 运行相关测试，或在 `searchByTargets` 中为每个 goroutine 创建独立的 context。

### 方向 3：Qdrant gRPC 连接并发行为（优先级中）

Go gRPC 客户端通过单个连接多路复用请求。如果 Qdrant 服务端在处理并发请求时存在 bug（特别是不同 collection filter 的并发搜索），可能返回空结果。

**验证方法**：直接用 gRPC 客户端工具（如 grpcurl）并发发送两个不同 filter 的搜索请求到 Qdrant。

### 方向 4：Pipeline 后续阶段问题（优先级中）

即使搜索阶段返回了结果，CHUNK_RERANK、CHUNK_MERGE、FILTER_TOP_K 阶段也可能在处理来自多个 KB 的混合结果时出错。

**验证方法**：在 pipeline 各阶段之间添加日志，记录每阶段输入输出的结果数量。

### 方向 5：1.321s 耗时异常的根因（优先级高）

失败场景的 1.321s 响应时间异常短，推测搜索流程在早期就中断了。可能原因：
- embedding API 返回错误被吞掉
- goroutine panic 被 recover 捕获但未正确传播
- 某个前置检查（如 KB 权限/存在性校验）导致提前返回空

**验证方法**：查看 `docker logs WeKnora-app` 中多 KB 搜索对应的完整日志链路。

---

## 已实施的代码变更

| 文件 | 变更 | Commit | 效果 |
|------|------|--------|------|
| `internal/types/evaluation.go` | SafeJieba 互斥锁封装 | 89e9529 | ❌ 未修复 bug |
| `internal/application/service/retriever/composite.go` | concurrentRetrieve 收集部分结果 | 89e9529 | ❌ 未修复 bug |
| `config/config.yaml` | vector_threshold 0.5→0.3 | 89e9529 | ⚠️ 单 KB 改善，多 KB 恶化 |
| `internal/application/repository/retriever/qdrant/repository.go` | 搜索诊断日志 | 89e9529 | 辅助诊断 |

**注意**：`vector_threshold` 从 0.5 改为 0.3 导致 K8S 多 KB 搜索恶化（从 4 条变为 0 条），可能需要回退。

---

## 附录：一键测试脚本

```bash
#!/bin/bash
# multi-kb-search-test.sh
# 多知识库搜索 Bug 测试脚本
#
# 用法: bash multi-kb-search-test.sh [SERVER_URL]
# 示例: bash multi-kb-search-test.sh http://192.168.100.30:8080

SERVER=${1:-"http://192.168.100.30:8080"}
KB1="7ac85a8f-482d-4b95-a99b-8b07cf41eab7"
KB2="2d2ee8a8-703f-418d-88c7-94321b82a866"
FAKE_KB="00000000-0000-0000-0000-000000000000"
QUERY="docker"
EMAIL="longwz20047@gmail.com"
PASSWORD="ywl20047"

echo "=== 多知识库搜索 Bug 测试 ==="
echo "服务器: $SERVER"
echo ""

# 登录获取 token
echo "[1/7] 登录获取 Token..."
LOGIN_RESP=$(curl -s -X POST "$SERVER/api/v1/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}")

TOKEN=$(echo "$LOGIN_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['access_token'])" 2>/dev/null)

if [ -z "$TOKEN" ]; then
  echo "登录失败！响应: $LOGIN_RESP"
  exit 1
fi
echo "Token 获取成功"
echo ""

# 搜索函数
search() {
  local desc="$1"
  local kb_ids="$2"
  local start_time=$(date +%s%3N)

  local resp=$(curl -s -X POST "$SERVER/api/v1/knowledge-search" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"query\":\"$QUERY\",\"knowledge_base_ids\":$kb_ids}")

  local end_time=$(date +%s%3N)
  local duration=$((end_time - start_time))

  local count=$(echo "$resp" | python3 -c "
import sys,json
try:
    d=json.load(sys.stdin)['data']
    print(len(d) if isinstance(d,list) else 0)
except:
    print(-1)
" 2>/dev/null)

  printf "  %-25s → %3s 条结果  (%d ms)\n" "$desc" "$count" "$duration"
}

# 执行测试
echo "[2/7] 单知识库搜索（对照组）"
search "KB1 单独" "[\"$KB1\"]"
search "KB2 单独" "[\"$KB2\"]"
echo ""

echo "[3/7] 多知识库搜索（Bug 复现）"
search "KB1 + KB2" "[\"$KB1\",\"$KB2\"]"
search "KB2 + KB1（调换顺序）" "[\"$KB2\",\"$KB1\"]"
echo ""

echo "[4/7] 同一 KB 重复（预期正常）"
search "KB1 x2" "[\"$KB1\",\"$KB1\"]"
search "KB2 x2" "[\"$KB2\",\"$KB2\"]"
echo ""

echo "[5/7] 不存在 KB + 真实 KB（预期正常）"
search "fake + KB1" "[\"$FAKE_KB\",\"$KB1\"]"
search "fake + KB2" "[\"$FAKE_KB\",\"$KB2\"]"
echo ""

echo "[6/7] 稳定性验证（连续 3 次多 KB 搜索）"
for i in 1 2 3; do
  search "KB1+KB2 第${i}次" "[\"$KB1\",\"$KB2\"]"
done
echo ""

echo "[7/7] 测试完成"
echo ""
echo "=== 预期结果 ==="
echo "  - 单 KB 搜索: > 0 条结果"
echo "  - 多 KB 搜索 (KB1+KB2): 应该 > 0（若为 0 则 bug 存在）"
echo "  - 同 KB 重复: > 0 条结果"
echo "  - fake + 真实 KB: > 0 条结果"
echo "  - 多 KB 搜索耗时应 >= 单 KB（若显著更短则有异常）"
```
