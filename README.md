# Agent MCP Gateway

**Version:** 1.0.0 | **Go:** 1.22+ | **License:** MIT | **Protocol:** MCP (Model Context Protocol)

---

## Overview

Agent MCP Gateway is a secure gateway server that exposes internal database resources to AI agents through the Model Context Protocol (MCP). It enables AI-powered workflows to query Redis, MongoDB, MySQL, and Elasticsearch backends hosted on private networks, without requiring direct network access from the AI agent or its host machine.

The gateway embeds a WireGuard userspace tunnel (netstack) to reach internal infrastructure. This approach runs entirely in userspace with no root privileges, no TUN device, and no kernel module required. All traffic between the gateway and backend databases flows through an encrypted WireGuard tunnel over the internal network.

Authentication is handled through Okta SSO using OIDC with PKCE. Authorization follows a role-based access control (RBAC) model driven by Okta group memberships, allowing fine-grained control over which teams can access which resources and what operations they may perform. All usage is metered and recorded in a local SQLite database for auditing and chargeback.

## Architecture

```
┌─────────────┐     HTTPS/MCP      ┌──────────────────────────────────────┐
│   AI Agent  │ <────────────────-> │          QA MCP Gateway              │
│  (Claude,   │    Streamable HTTP  │                                      │
│   GPT, etc) │                     │  ┌──────────┐  ┌──────────────────┐  │
└─────────────┘                     │  │ Okta OIDC │  │    RBAC Engine   │  │
      │                             │  │   Auth    │  │  (group->perms)  │  │
      │ localhost:3000/mcp          │  └──────────┘  └──────────────────┘  │
      v                             │                                      │
┌─────────────┐                     │  ┌──────────────────────────────────┐│
│ agent-mcp-gateway│  -- SSO Login --->  │  │     MCP Tool Handlers           ││
│   connect   │  -- Bootstrap --->  │  │  list_resources | redis_query   ││
│  (client)   │                     │  │  mongo_query | mysql_query      ││
└─────────────┘                     │  │  es_search   | get_usage        ││
                                    │  └──────────────────────────────────┘│
                                    │                                      │
                                    │  ┌──────────────────────────────────┐│
                                    │  │  WireGuard Tunnel (netstack)     ││
                                    │  │  Userspace - No root - No TUN   ││
                                    │  └───────────┬──────────────────────┘│
                                    └──────────────┼───────────────────────┘
                                                   │ 10.0.1.0/24
                            ┌──────────┬───────────┼──────────┬──────────┐
                            v          v           v          v          │
                        ┌───────┐ ┌─────────┐ ┌─────────┐ ┌──────┐     │
                        │ Redis │ │ MongoDB │ │  MySQL  │ │  ES  │     │
                        └───────┘ └─────────┘ └─────────┘ └──────┘     │
                                    Internal Network                 │
```

## Features

- **MCP Protocol Support** -- Exposes databases as MCP tools over Streamable HTTP transport.
- **Multi-Backend Support** -- Query Redis, MongoDB, MySQL, and Elasticsearch from a single gateway.
- **WireGuard Userspace Tunnel** -- Embedded netstack-based WireGuard tunnel requires no root privileges, no TUN device, and no kernel modules.
- **Okta SSO Authentication** -- OIDC with PKCE flow for secure, browser-based single sign-on.
- **Role-Based Access Control** -- Map Okta groups to resource permissions with read/write granularity.
- **Operation Whitelisting** -- Each resource defines an explicit list of allowed operations.
- **Read-Only Mode** -- Resources can be locked to read-only access to protect data integrity.
- **Usage Metering** -- All queries are recorded in a local SQLite database for auditing and usage tracking.
- **Client/Server Architecture** -- `agent-mcp-gateway serve` runs the gateway server; `agent-mcp-gateway connect` provides a local MCP proxy for AI agents.
- **Auto TLS** -- Automatic certificate provisioning via Let's Encrypt.
- **Docker Support** -- Multi-stage Dockerfile for minimal production images.
- **Environment Variable Expansion** -- Secrets in configuration files can reference environment variables with `${VAR}` syntax.

## Quick Start

### Prerequisites

- **Go 1.22+** for building from source
- A **configuration file** based on `config.example.yaml`
- A **WireGuard peer** on the QA network configured to accept connections from the gateway
- An **Okta application** configured for OIDC with PKCE (SPA or Native type)

### Build

```bash
make build
```

The binary is written to `bin/agent-mcp-gateway`.

### Configure

```bash
cp config.example.yaml config.yaml
```

Edit `config.yaml` to set your Okta issuer, WireGuard keys, and resource connection strings. Use environment variables for secrets:

```bash
export REDIS_PASSWORD="your-redis-password"
export MONGO_PASSWORD="your-mongo-password"
export MYSQL_PASSWORD="your-mysql-password"
```

### Run the Server

```bash
./bin/agent-mcp-gateway serve --config config.yaml
```

### Connect a Client

On a developer or CI machine, run the client to create a local MCP endpoint:

```bash
./bin/agent-mcp-gateway connect --server https://agent-mcp-gateway.example.com
```

This opens a browser for Okta SSO login, then starts a local MCP proxy on `localhost:3000/mcp` that AI agents can connect to.

## MCP Tools Reference

| Tool | Description | Parameters |
|------|-------------|------------|
| `list_resources` | List all resources the authenticated user can access | None |
| `redis_query` | Execute a Redis command on a named resource | `resource` (string), `command` (string), `args` (array of strings) |
| `mongo_query` | Run a MongoDB query or aggregation | `resource` (string), `operation` (string: find, aggregate, count, listCollections, distinct), `collection` (string), `filter` (object), `pipeline` (array, for aggregate), `options` (object) |
| `mysql_query` | Execute a SQL query against MySQL | `resource` (string), `query` (string), `params` (array) |
| `es_search` | Search or query Elasticsearch | `resource` (string), `operation` (string: search, cat, indices, count), `index` (string), `body` (object) |
| `get_usage` | Retrieve usage statistics for the current user | `days` (integer, default 30) |

All tools enforce RBAC permissions. If the authenticated user does not have access to the requested resource or operation, the tool returns an authorization error.

## Configuration

The configuration file is YAML-based. Each top-level section controls a different aspect of the gateway.

### `server`

Network listener settings. Set `tls: auto` for automatic Let's Encrypt certificates or `tls: off` for plain HTTP (useful behind a reverse proxy).

### `auth`

Authentication and authorization settings. The `okta` block configures OIDC parameters. The `rbac` block maps Okta groups to resource names and permission levels (`read`, `write`). Use `"*"` as a resource wildcard for admin groups.

### `wireguard`

WireGuard tunnel configuration. Provide the gateway's private key, the overlay network address, and the peer endpoint details. The `keepalive` value (in seconds) maintains NAT traversal.

### `resources`

Named backend resources. Each resource specifies a `type` (redis, mongodb, mysql, elasticsearch), connection parameters, a human-readable `description`, and an `allowed_ops` whitelist. Set `read_only: true` to prevent mutating operations.

### `clients`

Client version enforcement and WireGuard IP pool allocation for client tunnels. The `auto_provision` flag controls whether client WireGuard keys are automatically generated and assigned.

### `logging`

Log output settings. Supports `json` and `text` formats. Set `level` to `debug`, `info`, `warn`, or `error`.

### `metering`

Usage metering configuration. When enabled, all tool invocations are recorded to a SQLite database at the specified path.

## Development

### Run Tests

```bash
make test
```

Runs all tests with race detection and coverage summary.

### Run Linter

```bash
make lint
```

Runs `go vet` followed by `staticcheck`. Installs `staticcheck` automatically if not present.

### Coverage Report

```bash
make test-coverage
```

Generates an HTML coverage report at `coverage.html`.

### Format Code

```bash
make fmt
```

### Tidy Dependencies

```bash
make tidy
```

## Docker

### Build the Image

```bash
make docker
```

Or manually:

```bash
docker build -t agent-mcp-gateway:latest .
```

### Run with Docker

```bash
docker run -d \
  --name agent-mcp-gateway \
  -p 443:443 \
  -p 80:80 \
  -v $(pwd)/config.yaml:/etc/agent-mcp-gateway/config.yaml:ro \
  -v agent-mcp-gateway-data:/var/lib/agent-mcp-gateway \
  -e REDIS_PASSWORD="secret" \
  -e MONGO_PASSWORD="secret" \
  -e MYSQL_PASSWORD="secret" \
  agent-mcp-gateway:latest
```

The container runs as a non-root user (`gateway`, UID 1000). The metering SQLite database is stored in the `agent-mcp-gateway-data` volume at `/var/lib/agent-mcp-gateway/meter.db`.

### Docker Compose

For production deployments, create a `docker-compose.yaml`:

```yaml
services:
  agent-mcp-gateway:
    build: .
    ports:
      - "443:443"
      - "80:80"
    volumes:
      - ./config.yaml:/etc/agent-mcp-gateway/config.yaml:ro
      - gateway-data:/var/lib/agent-mcp-gateway
    env_file:
      - .env
    restart: unless-stopped

volumes:
  gateway-data:
```

## Security

### Authentication

All requests require a valid Okta SSO session. The gateway performs OIDC token validation against the configured issuer, verifying signature, audience, and expiration. The client mode uses PKCE to obtain tokens without exposing client secrets.

### Authorization

RBAC rules map Okta groups to resource permissions. Each tool invocation checks the authenticated user's group memberships against the configured rules before executing any backend query. Wildcard (`*`) resource matching is supported for admin groups.

### Network Security

The WireGuard tunnel encrypts all traffic between the gateway and QA backends. The tunnel runs in userspace via netstack, requiring no elevated privileges. Backend connection strings and passwords are resolved from environment variables at startup and never logged.

### Operation Whitelisting

Each resource defines an explicit list of allowed operations. Any operation not in the whitelist is rejected before reaching the backend. Combined with `read_only: true`, this prevents accidental or malicious data mutation.

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
