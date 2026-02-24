# QA MCP Gateway - 完整项目

## 项目概述
用 Go 写一个 MCP（Model Context Protocol）协议网关，内嵌 WireGuard（netstack 用户态模式），用于让外部 AI Agent 安全访问 QA 内网数据库资源。

## 核心特性
1. **MCP Server**: 实现 MCP 协议（Streamable HTTP），对外暴露统一接口
2. **WireGuard netstack**: 用户态 WireGuard，不需要 root，不污染系统路由
3. **多数据库支持**: Redis、MongoDB、MySQL、Elasticsearch
4. **Okta SSO 认证**: OIDC/PKCE flow，客户端零配置
5. **RBAC 权限控制**: 基于 Okta group 映射资源和操作权限
6. **客户端零配置**: `qa-gateway connect` 一条命令，自动 SSO 登录 + 拉取 WG 配置 + 启动本地 MCP proxy
7. **自动更新**: 客户端版本检查，提示更新/强制更新/自更新
8. **日志 & 计量**: 记录 who/when/resource/operation/bytes/latency
9. **TLS**: Let's Encrypt autocert

## MCP Tools（Agent 可调用）
- `list_resources` — 列出所有可用资源及描述
- `redis_query` — 执行 Redis 命令 (GET/SET/KEYS/SCAN/DEL/TTL/INFO)
- `mongo_query` — 执行 MongoDB 查询 (find/aggregate/count/listCollections/distinct)
- `mysql_query` — 执行 SQL (SELECT/SHOW/DESCRIBE/EXPLAIN)
- `es_search` — 执行 ES 查询 DSL (search/cat/indices/count)
- `get_usage` — 查看自己的用量统计

## 架构

### 服务端 (`qa-gateway serve`)
- 公网 HTTPS，MCP Streamable HTTP 端点 `/mcp`
- Okta OIDC 验证 JWT
- RBAC 中间件检查权限
- 通过内嵌 netstack WireGuard 连接内网数据库
- SQLite 记录访问日志和用量

### 客户端 (`qa-gateway connect`)
1. 打开浏览器 → Okta SSO 登录（PKCE）
2. 获得 JWT → 调用服务端 `/api/bootstrap` 拉取 WG 配置 + 资源列表 + 版本信息
3. 启动 netstack WireGuard + 本地 MCP proxy（localhost:3000/mcp）
4. Agent 连 localhost:3000/mcp 即可

### 自更新
- `/api/bootstrap` 返回 client_version（current/minimum/latest）
- client < minimum → 拒绝连接，强制更新
- client < latest → 提示更新
- `qa-gateway update` 自更新命令

## 配置文件 (config.yaml)
```yaml
server:
  listen: ":443"
  domain: "qa-gateway.yourcompany.com"
  tls: auto

auth:
  provider: okta
  okta:
    issuer: "https://company.okta.com/oauth2/default"
    client_id: "0oa..."
    scopes: ["openid", "profile", "email", "groups"]
  rbac:
    - group: "qa-team"
      resources: ["qa-redis", "qa-mongo", "qa-mysql", "qa-elasticsearch"]
      permissions: [read]
    - group: "qa-admin"
      resources: ["*"]
      permissions: [read, write]

wireguard:
  private_key: "xxx"
  address: "10.0.1.100/32"
  dns: "10.0.1.1"
  peer:
    public_key: "yyy"
    endpoint: "qa-vpn.internal.com:51820"
    allowed_ips: "10.0.1.0/24"
    keepalive: 25

resources:
  qa-redis:
    type: redis
    host: "10.0.1.50:6379"
    password: "${REDIS_PASSWORD}"
    description: "QA Redis 缓存"
    read_only: true
    allowed_ops: [get, set, keys, del, ttl, info, scan]
  qa-mongo:
    type: mongodb
    uri: "mongodb://ro_user:${MONGO_PASSWORD}@10.0.1.51:27017/qa_db"
    description: "QA MongoDB 主库"
    read_only: true
    allowed_ops: [find, aggregate, count, listCollections, distinct]
  qa-mysql:
    type: mysql
    dsn: "readonly:${MYSQL_PASSWORD}@tcp(10.0.1.52:3306)/qa_app"
    description: "QA MySQL 应用库"
    read_only: true
    allowed_ops: [select, show, describe, explain]
  qa-elasticsearch:
    type: elasticsearch
    url: "http://10.0.1.53:9200"
    description: "QA ES 日志"
    read_only: true
    allowed_ops: [search, cat, indices, count]

clients:
  version:
    latest: "1.0.0"
    minimum: "1.0.0"
    download_url: "https://releases.company.com/qa-gateway/"
  wireguard:
    pool: "10.0.1.200/24"
    auto_provision: true

logging:
  level: info
  file: "/var/log/qa-gateway/access.log"
  format: json

metering:
  enabled: true
  storage: sqlite
  db_path: "/var/lib/qa-gateway/meter.db"
```

## 技术选型
- Go 1.22+
- MCP SDK: github.com/mark3labs/mcp-go
- WireGuard: golang.zx2c4.com/wireguard + gvisor netstack
- Config: gopkg.in/yaml.v3 + envsubst
- Auth: github.com/coreos/go-oidc/v3
- Redis: github.com/redis/go-redis/v9
- MongoDB: go.mongodb.org/mongo-driver
- MySQL: github.com/go-sql-driver/mysql
- ES: github.com/elastic/go-elasticsearch/v8
- TLS: golang.org/x/crypto/acme/autocert
- Metering: modernc.org/sqlite (CGO-free)
- CLI: github.com/spf13/cobra

## 项目结构
```
qa-mcp-gateway/
├── cmd/
│   └── qa-gateway/
│       └── main.go
├── internal/
│   ├── config/          # 配置加载
│   ├── auth/            # Okta OIDC + RBAC
│   ├── tunnel/          # WireGuard netstack
│   ├── mcp/             # MCP server + tools
│   ├── resources/       # 数据库连接器 (redis/mongo/mysql/es)
│   ├── metering/        # 用量统计
│   ├── server/          # HTTP server + middleware
│   └── client/          # 客户端 connect/update 逻辑
├── config.example.yaml
├── go.mod
├── go.sum
├── Makefile
├── Dockerfile
├── README.md
└── *_test.go            # 每个包都要有单元测试
```

## 要求
1. 业界最高标准代码质量
2. 完整单元测试（每个包），测试覆盖率 > 80%
3. 优秀的 README（英文，有架构图、快速开始、配置说明、开发指南）
4. 合理的错误处理，不要 panic
5. 结构化日志 (slog)
6. 优雅关闭 (graceful shutdown)
7. 接口抽象（方便测试 mock）
8. Makefile（build/test/lint/docker）
9. Dockerfile（多阶段构建）
10. CI-ready（go vet, staticcheck, tests）

请按上述需求完整实现项目。先创建项目结构，然后逐个实现每个包，最后确保 `go build` 和 `go test ./...` 都能通过。
