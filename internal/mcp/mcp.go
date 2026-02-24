// Package mcp creates and configures the MCP server with all tool registrations.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	gomcp "github.com/mark3labs/mcp-go/mcp"
	gomcpserver "github.com/mark3labs/mcp-go/server"

	"qa-mcp-gateway/internal/auth"
	"qa-mcp-gateway/internal/metering"
	"qa-mcp-gateway/internal/resources"
)

// NewServer creates an MCPServer with all gateway tools registered. The tools
// provide access to backend resources through the MCP protocol, with
// authorization checks and usage metering on every request.
func NewServer(
	name, version string,
	mgr *resources.Manager,
	authz auth.Authorizer,
	meter metering.Meter,
	logger *slog.Logger,
) *gomcpserver.MCPServer {
	srv := gomcpserver.NewMCPServer(name, version)

	// list_resources — returns the resources the caller is allowed to access.
	listResourcesTool := gomcp.NewTool("list_resources",
		gomcp.WithDescription("List all resources available to the authenticated user"),
	)
	srv.AddTool(listResourcesTool, makeListResourcesHandler(mgr, authz, logger))

	// redis_query — execute operations on a Redis resource.
	redisQueryTool := gomcp.NewTool("redis_query",
		gomcp.WithDescription("Execute a Redis operation on a named resource"),
		gomcp.WithString("resource", gomcp.Required(), gomcp.Description("Name of the Redis resource")),
		gomcp.WithString("operation", gomcp.Required(), gomcp.Description("Redis operation to perform"),
			gomcp.Enum("get", "set", "keys", "del", "ttl", "info", "scan")),
		gomcp.WithString("key", gomcp.Description("Redis key")),
		gomcp.WithString("value", gomcp.Description("Value to set")),
		gomcp.WithString("pattern", gomcp.Description("Key pattern for keys/scan operations")),
		gomcp.WithNumber("ttl", gomcp.Description("TTL in seconds for set operations")),
		gomcp.WithString("section", gomcp.Description("INFO section name")),
		gomcp.WithNumber("count", gomcp.Description("Count hint for scan operations")),
	)
	srv.AddTool(redisQueryTool, makeQueryHandler(mgr, authz, meter, logger))

	// mongo_query — execute operations on a MongoDB resource.
	mongoQueryTool := gomcp.NewTool("mongo_query",
		gomcp.WithDescription("Execute a MongoDB operation on a named resource"),
		gomcp.WithString("resource", gomcp.Required(), gomcp.Description("Name of the MongoDB resource")),
		gomcp.WithString("operation", gomcp.Required(), gomcp.Description("MongoDB operation to perform"),
			gomcp.Enum("find", "aggregate", "count", "listCollections", "distinct")),
		gomcp.WithString("collection", gomcp.Description("MongoDB collection name")),
		gomcp.WithObject("filter", gomcp.Description("Query filter document")),
		gomcp.WithArray("pipeline", gomcp.Description("Aggregation pipeline stages")),
		gomcp.WithString("field", gomcp.Description("Field name for distinct operation")),
		gomcp.WithNumber("limit", gomcp.Description("Maximum number of results")),
	)
	srv.AddTool(mongoQueryTool, makeQueryHandler(mgr, authz, meter, logger))

	// mysql_query — execute operations on a MySQL resource.
	mysqlQueryTool := gomcp.NewTool("mysql_query",
		gomcp.WithDescription("Execute a MySQL operation on a named resource"),
		gomcp.WithString("resource", gomcp.Required(), gomcp.Description("Name of the MySQL resource")),
		gomcp.WithString("operation", gomcp.Required(), gomcp.Description("MySQL operation to perform"),
			gomcp.Enum("select", "show", "describe", "explain")),
		gomcp.WithString("query", gomcp.Description("SQL query string")),
		gomcp.WithString("table", gomcp.Description("Table name for describe operation")),
	)
	srv.AddTool(mysqlQueryTool, makeQueryHandler(mgr, authz, meter, logger))

	// es_search — execute operations on an Elasticsearch resource.
	esSearchTool := gomcp.NewTool("es_search",
		gomcp.WithDescription("Execute an Elasticsearch operation on a named resource"),
		gomcp.WithString("resource", gomcp.Required(), gomcp.Description("Name of the Elasticsearch resource")),
		gomcp.WithString("operation", gomcp.Required(), gomcp.Description("Elasticsearch operation to perform"),
			gomcp.Enum("search", "cat", "indices", "count")),
		gomcp.WithString("index", gomcp.Description("Elasticsearch index name")),
		gomcp.WithObject("body", gomcp.Description("Request body for search/count operations")),
		gomcp.WithString("endpoint", gomcp.Description("Cat endpoint name")),
	)
	srv.AddTool(esSearchTool, makeQueryHandler(mgr, authz, meter, logger))

	// get_usage — returns usage statistics for the authenticated user.
	getUsageTool := gomcp.NewTool("get_usage",
		gomcp.WithDescription("Get usage statistics for the authenticated user"),
		gomcp.WithNumber("days", gomcp.Description("Number of days to look back (default 30)")),
	)
	srv.AddTool(getUsageTool, makeGetUsageHandler(meter, logger))

	return srv
}

// makeListResourcesHandler returns a handler that lists all resources the
// caller is authorized to access.
func makeListResourcesHandler(
	mgr *resources.Manager,
	authz auth.Authorizer,
	logger *slog.Logger,
) gomcpserver.ToolHandlerFunc {
	return func(ctx context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
		claims, ok := auth.ClaimsFromContext(ctx)
		if !ok {
			return gomcp.NewToolResultError("unauthorized: no credentials in context"), nil
		}

		allowed := authz.AllowedResources(claims)
		allowedSet := make(map[string]struct{}, len(allowed))
		for _, name := range allowed {
			allowedSet[name] = struct{}{}
		}

		allResources := mgr.List()
		var filtered []resources.ResourceInfo
		for name, info := range allResources {
			// Allow access if the resource name is explicitly listed or if
			// the wildcard "*" was granted.
			if _, ok := allowedSet[name]; ok {
				filtered = append(filtered, info)
			} else if _, ok := allowedSet["*"]; ok {
				filtered = append(filtered, info)
			}
		}

		data, err := json.Marshal(filtered)
		if err != nil {
			logger.Error("failed to marshal resource list", "error", err)
			return gomcp.NewToolResultError("internal error: failed to marshal resources"), nil
		}

		return gomcp.NewToolResultText(string(data)), nil
	}
}

// makeQueryHandler returns a generic handler that authorizes the caller,
// executes an operation on a named resource, records metering, and returns
// the JSON-encoded result.
func makeQueryHandler(
	mgr *resources.Manager,
	authz auth.Authorizer,
	meter metering.Meter,
	logger *slog.Logger,
) gomcpserver.ToolHandlerFunc {
	return func(ctx context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
		// 1. Authenticate.
		claims, ok := auth.ClaimsFromContext(ctx)
		if !ok {
			return gomcp.NewToolResultError("unauthorized: no credentials in context"), nil
		}

		args := request.GetArguments()

		// 2. Extract resource name.
		resourceName, _ := args["resource"].(string)
		if resourceName == "" {
			return gomcp.NewToolResultError("resource parameter is required"), nil
		}

		// 3. Extract operation.
		operation, _ := args["operation"].(string)
		if operation == "" {
			return gomcp.NewToolResultError("operation parameter is required"), nil
		}

		// 4. Authorize.
		if !authz.Check(claims, resourceName, "read") {
			return gomcp.NewToolResultError(fmt.Sprintf("forbidden: no access to resource %q", resourceName)), nil
		}

		// 5. Look up resource.
		res, found := mgr.Get(resourceName)
		if !found {
			return gomcp.NewToolResultError(fmt.Sprintf("resource %q not found", resourceName)), nil
		}

		// 6. Build params map from all request arguments (excluding meta keys).
		params := make(map[string]any)
		for k, v := range args {
			if k == "resource" || k == "operation" {
				continue
			}
			params[k] = v
		}

		// 7. Execute with timing.
		start := time.Now()
		result, execErr := res.Execute(ctx, operation, params)
		elapsed := time.Since(start)

		// 8. Record metering.
		if meter != nil {
			entry := &metering.UsageEntry{
				UserID:    claims.Subject,
				Email:     claims.Email,
				Resource:  resourceName,
				Operation: operation,
				Latency:   elapsed,
				Timestamp: time.Now(),
				Success:   execErr == nil,
			}
			if execErr != nil {
				entry.Error = execErr.Error()
			}
			if mErr := meter.Record(ctx, entry); mErr != nil {
				logger.Error("failed to record metering", "error", mErr)
			}
		}

		// 9. Handle execution error.
		if execErr != nil {
			return gomcp.NewToolResultError(fmt.Sprintf("execution error: %v", execErr)), nil
		}

		// 10. Marshal result to JSON.
		data, err := json.Marshal(result)
		if err != nil {
			logger.Error("failed to marshal result", "error", err)
			return gomcp.NewToolResultError("internal error: failed to marshal result"), nil
		}

		return gomcp.NewToolResultText(string(data)), nil
	}
}

// makeGetUsageHandler returns a handler that retrieves usage statistics for
// the authenticated user.
func makeGetUsageHandler(
	meter metering.Meter,
	logger *slog.Logger,
) gomcpserver.ToolHandlerFunc {
	return func(ctx context.Context, request gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
		claims, ok := auth.ClaimsFromContext(ctx)
		if !ok {
			return gomcp.NewToolResultError("unauthorized: no credentials in context"), nil
		}

		if meter == nil {
			return gomcp.NewToolResultError("metering is not enabled"), nil
		}

		args := request.GetArguments()
		days := 30.0
		if d, ok := args["days"].(float64); ok && d > 0 {
			days = d
		}

		to := time.Now()
		from := to.AddDate(0, 0, -int(days))

		stats, err := meter.GetUsage(ctx, claims.Subject, from, to)
		if err != nil {
			logger.Error("failed to get usage", "error", err)
			return gomcp.NewToolResultError(fmt.Sprintf("failed to get usage: %v", err)), nil
		}

		data, err := json.Marshal(stats)
		if err != nil {
			logger.Error("failed to marshal usage stats", "error", err)
			return gomcp.NewToolResultError("internal error: failed to marshal usage stats"), nil
		}

		return gomcp.NewToolResultText(string(data)), nil
	}
}
