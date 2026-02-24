# Agent MCP Gateway — Agent Integration Guide

This document is intended for AI agents (Claude, GPT, Codex, OpenClaw, etc.) and developers integrating agents with Agent MCP Gateway.

## What This Gateway Does

Agent MCP Gateway gives you secure access to internal databases (Redis, MongoDB, MySQL, Elasticsearch) through a single MCP endpoint. You don't need direct network access, VPN clients, or database credentials — the gateway handles all of that.

## Connecting

### Option 1: Local Client (Recommended)

If `agent-mcp-gateway` is installed on the machine running your agent:

```bash
agent-mcp-gateway connect --server https://gateway.example.com
```

This opens a browser for SSO login (or uses device code flow on headless machines — see below), then starts a local MCP proxy.

**Your MCP endpoint:** `http://localhost:3000/mcp`

Configure your agent:

```json
{
  "mcpServers": {
    "internal": {
      "url": "http://localhost:3000/mcp"
    }
  }
}
```

No API keys or tokens needed — authentication is handled by the SSO session.

### Option 2: Direct (Remote Gateway)

If the gateway is exposed directly:

```json
{
  "mcpServers": {
    "internal": {
      "url": "https://gateway.example.com/mcp",
      "headers": {
        "Authorization": "Bearer <your-token>"
      }
    }
  }
}
```

## Available Tools

Once connected, you can discover tools via `tools/list`. The gateway exposes:

### `list_resources`

Lists all database resources you have access to.

**Parameters:** None

**Example response:**
```json
[
  {"name": "redis-cache", "type": "redis", "description": "Redis cache"},
  {"name": "mongo-main", "type": "mongodb", "description": "MongoDB primary"},
  {"name": "mysql-app", "type": "mysql", "description": "MySQL application database"},
  {"name": "es-logs", "type": "elasticsearch", "description": "Elasticsearch logs"}
]
```

### `redis_query`

Execute a Redis command.

| Parameter  | Type     | Required | Description                        |
|-----------|----------|----------|------------------------------------|
| `resource` | string   | Yes      | Resource name from `list_resources` |
| `command`  | string   | Yes      | Redis command (GET, SET, KEYS, SCAN, TTL, INFO, DEL) |
| `args`     | string[] | No       | Command arguments                  |

**Examples:**
```
redis_query(resource="redis-cache", command="GET", args=["user:123"])
redis_query(resource="redis-cache", command="KEYS", args=["session:*"])
redis_query(resource="redis-cache", command="SCAN", args=["0", "MATCH", "order:*", "COUNT", "100"])
```

### `mongo_query`

Run a MongoDB query or aggregation.

| Parameter    | Type   | Required | Description                        |
|-------------|--------|----------|------------------------------------|
| `resource`   | string | Yes      | Resource name                      |
| `operation`  | string | Yes      | One of: find, aggregate, count, listCollections, distinct |
| `collection` | string | Yes*     | Collection name (*not needed for listCollections) |
| `filter`     | object | No       | Query filter (for find, count)     |
| `pipeline`   | array  | No       | Aggregation pipeline (for aggregate) |
| `options`    | object | No       | Additional options (limit, sort, projection) |

**Examples:**
```
mongo_query(resource="mongo-main", operation="find", collection="users", filter={"active": true}, options={"limit": 10, "sort": {"created_at": -1}})
mongo_query(resource="mongo-main", operation="aggregate", collection="orders", pipeline=[{"$group": {"_id": "$status", "count": {"$sum": 1}}}])
mongo_query(resource="mongo-main", operation="listCollections")
```

### `mysql_query`

Execute a SQL query.

| Parameter | Type   | Required | Description              |
|----------|--------|----------|--------------------------|
| `resource` | string | Yes     | Resource name             |
| `query`    | string | Yes     | SQL query (SELECT, SHOW, DESCRIBE, EXPLAIN only) |
| `params`   | array  | No      | Query parameters for prepared statements |

**Examples:**
```
mysql_query(resource="mysql-app", query="SELECT * FROM users WHERE created_at > ? LIMIT 10", params=["2024-01-01"])
mysql_query(resource="mysql-app", query="SHOW TABLES")
mysql_query(resource="mysql-app", query="DESCRIBE users")
```

### `es_search`

Search Elasticsearch.

| Parameter   | Type   | Required | Description              |
|------------|--------|----------|--------------------------|
| `resource`  | string | Yes      | Resource name             |
| `operation` | string | Yes      | One of: search, cat, indices, count |
| `index`     | string | No       | Index name or pattern     |
| `body`      | object | No       | Query DSL body            |

**Examples:**
```
es_search(resource="es-logs", operation="search", index="app-logs-*", body={"query": {"match": {"level": "error"}}, "size": 20, "sort": [{"@timestamp": "desc"}]})
es_search(resource="es-logs", operation="count", index="app-logs-*", body={"query": {"range": {"@timestamp": {"gte": "now-1h"}}}})
es_search(resource="es-logs", operation="indices")
```

### `get_usage`

Check your usage statistics.

| Parameter | Type    | Required | Description              |
|----------|---------|----------|--------------------------|
| `days`    | integer | No       | Lookback period (default: 30) |

## Best Practices for Agents

1. **Start with `list_resources`** to discover what's available and check your permissions.
2. **Use specific queries** — avoid `SELECT *` or unbounded scans. Add `LIMIT` to SQL and `size` to ES queries.
3. **Prefer read operations** — most resources are configured as read-only. Mutation attempts will be rejected.
4. **Handle errors gracefully** — the gateway returns descriptive error messages for permission denials, invalid operations, and backend failures.
5. **Check `get_usage`** periodically if you're running automated workloads.

## Error Responses

| Error | Meaning |
|-------|---------|
| `unauthorized` | Missing or invalid authentication |
| `forbidden` | You don't have permission for this resource/operation |
| `resource not found` | The named resource doesn't exist or isn't visible to you |
| `operation not allowed` | The operation isn't in the resource's whitelist |
| `read-only resource` | Mutation attempted on a read-only resource |
| `backend error` | The underlying database returned an error |

## Headless / No-Browser Authentication

See the main README for device code flow details when running on machines without a browser.
