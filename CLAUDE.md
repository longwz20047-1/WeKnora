# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

WeKnora is a document understanding and semantic retrieval framework built on LLMs. It implements RAG (Retrieval-Augmented Generation) for intelligent document Q&A with support for multi-modal preprocessing, vector indexing, and LLM inference.

**Tech Stack**: Go 1.24 + Gin + GORM + PostgreSQL (pgvector) + Redis + DuckDB

## Development Commands

```bash
# Build and run
make build                    # Build binary
make run                      # Build and run
go run ./cmd/server           # Run directly

# Testing
make test                     # Run all tests
go test -v ./...              # Verbose test output
go test -v ./internal/application/service/metric/...  # Run specific package tests

# Code quality
make fmt                      # Format code
make lint                     # Run golangci-lint
make docs                     # Generate Swagger docs (requires: make install-swagger)

# Database migrations
make migrate-up               # Apply migrations
make migrate-down             # Rollback migrations
make migrate-create name=xxx  # Create new migration

# Development mode (recommended for local dev)
make dev-start                # Start infrastructure only (postgres, redis, etc.)
make dev-app                  # Run backend locally (after dev-start)
make dev-frontend             # Run frontend locally
make dev-stop                 # Stop development environment

# Docker
make start-all                # Start all services including Ollama
make stop-all                 # Stop all services
docker compose up -d          # Minimal startup
docker compose --profile full up -d  # Full features
```

## Architecture

### Core Application Flow

```
Request → Gin Router → Handler → Service → Repository → Database
                          ↓
                    Chat Pipeline (RAG)
                          ↓
              Agent Engine (ReACT loop with tools)
```

### Directory Structure

```
cmd/server/main.go           # Application entry point
internal/
├── agent/                   # ReACT Agent engine and tools
│   ├── engine.go            # Agent orchestration loop
│   └── tools/               # Built-in tools (knowledge_search, web_fetch, etc.)
├── application/
│   ├── repository/          # Data access layer
│   │   └── retriever/       # Vector search backends (postgres, elasticsearch, qdrant)
│   └── service/
│       ├── chat_pipline/    # RAG pipeline stages (search → rerank → generate)
│       ├── retriever/       # Composite retrieval strategies
│       └── metric/          # Evaluation metrics (BLEU, ROUGE, MRR, etc.)
├── container/               # Dependency injection (uber-go/dig)
├── handler/                 # HTTP handlers (Gin)
│   └── session/             # Chat session & streaming handlers
├── mcp/                     # MCP (Model Context Protocol) integration
├── models/                  # LLM/Embedding model abstractions
├── router/                  # Route definitions
└── stream/                  # SSE streaming manager
```

### Key Patterns

**Dependency Injection**: Uses `uber-go/dig` container. All services registered in `internal/container/container.go`.

**Chat Pipeline**: Modular pipeline in `internal/application/service/chat_pipline/`:
- `search.go` → `rerank.go` → `filter_top_k.go` → `chat_completion_stream.go`

**Agent Tools**: Implement `Tool` interface in `internal/agent/tools/`. Registry pattern for tool discovery.

**Vector Backends**: Pluggable via `RETRIEVE_DRIVER` env var:
- `postgres` (pgvector)
- `elasticsearch_v7` / `elasticsearch_v8`
- `qdrant`

### API Routes

Base path: `/api/v1`

- `/auth/*` - Authentication (JWT)
- `/chats_stream/*` - Chat with streaming (SSE)
- `/knowledgebases/*` - Knowledge base CRUD
- `/knowledges/*` - Document management
- `/models/*` - LLM/Embedding model config
- `/mcp/*` - MCP service management

Swagger docs available at `/swagger/index.html` (debug mode only).

## Environment Configuration

Copy `.env.example` to `.env`. Key variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `GIN_MODE` | `debug` or `release` | `release` |
| `DB_DRIVER` | Database type | `postgres` |
| `RETRIEVE_DRIVER` | Vector backend | `postgres` |
| `STORAGE_TYPE` | File storage (`local`/`minio`/`cos`) | `local` |
| `OLLAMA_BASE_URL` | Ollama service URL | `http://host.docker.internal:11434` |
| `ENABLE_GRAPH_RAG` | Enable knowledge graph | `false` |

## Testing

Tests are co-located with source files in `*_test.go`. Key test packages:

```bash
# Pipeline tests
go test -v ./internal/application/service/chat_pipline/...

# Metric calculation tests
go test -v ./internal/application/service/metric/...
```

## Related Projects

This backend serves:
- **weknora-ui**: Custom Vue 3 frontend (separate repo)
- **frontend/**: Built-in Vue frontend (in this repo)
