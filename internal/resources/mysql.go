package resources

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strings"

	"agent-gateway/internal/config"

	"github.com/go-sql-driver/mysql"
)

// MySQLResource implements the Resource interface for MySQL backends.
type MySQLResource struct {
	db         *sql.DB
	desc       string
	allowedOps []string
}

// newMySQLResource creates a new MySQLResource from the given configuration.
// The cfg.DSN should be a valid MySQL DSN.
// If dialFn is not nil, a custom dialer is registered and referenced in the DSN.
func newMySQLResource(name string, cfg config.ResourceConfig, dialFn DialContextFunc) (*MySQLResource, error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("mysql resource %q: DSN is required", name)
	}

	dsn := cfg.DSN

	if dialFn != nil {
		dialName := "wg-" + name
		mysql.RegisterDialContext(dialName, func(ctx context.Context, addr string) (net.Conn, error) {
			return dialFn(ctx, "tcp", addr)
		})
		// Prepend the dial name to the DSN so the driver uses our custom dialer.
		// The format is: [user[:password]@][net[(addr)]]/dbname[?param1=value1&...]
		// We need to ensure the net portion references our custom dialer.
		parsedDSN, err := mysql.ParseDSN(dsn)
		if err != nil {
			return nil, fmt.Errorf("mysql resource %q: parsing DSN: %w", name, err)
		}
		parsedDSN.Net = dialName
		dsn = parsedDSN.FormatDSN()
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("mysql resource %q: opening database: %w", name, err)
	}

	return &MySQLResource{
		db:         db,
		desc:       cfg.Description,
		allowedOps: cfg.AllowedOps,
	}, nil
}

// Type returns the resource type identifier.
func (r *MySQLResource) Type() string { return "mysql" }

// Description returns the human-readable description of this resource.
func (r *MySQLResource) Description() string { return r.desc }

// AllowedOps returns the list of allowed operations.
func (r *MySQLResource) AllowedOps() []string { return r.allowedOps }

// Close closes the underlying database connection.
func (r *MySQLResource) Close() error {
	if r.db != nil {
		return r.db.Close()
	}
	return nil
}

// Execute runs the specified operation on the MySQL backend.
// Supported ops: select, show, describe, explain.
func (r *MySQLResource) Execute(ctx context.Context, op string, params map[string]any) (any, error) {
	op = strings.ToLower(op)

	if !isOpAllowed(r.allowedOps, op) {
		return nil, fmt.Errorf("operation %q is not allowed on this resource", op)
	}

	switch op {
	case "select":
		return r.execSelect(ctx, params)
	case "show":
		return r.execShow(ctx, params)
	case "describe":
		return r.execDescribe(ctx, params)
	case "explain":
		return r.execExplain(ctx, params)
	default:
		return nil, fmt.Errorf("unsupported mysql operation: %s", op)
	}
}

func (r *MySQLResource) execSelect(ctx context.Context, params map[string]any) (any, error) {
	query, ok := params["query"].(string)
	if !ok || query == "" {
		return nil, fmt.Errorf("mysql select: 'query' parameter (string) is required")
	}

	// Validate that the query starts with SELECT to prevent injection.
	trimmed := strings.TrimSpace(query)
	if !strings.HasPrefix(strings.ToUpper(trimmed), "SELECT") {
		return nil, fmt.Errorf("mysql select: query must start with SELECT")
	}

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("mysql select: %w", err)
	}
	defer rows.Close()

	return scanRows(rows)
}

func (r *MySQLResource) execShow(ctx context.Context, params map[string]any) (any, error) {
	query, ok := params["query"].(string)
	if !ok || query == "" {
		return nil, fmt.Errorf("mysql show: 'query' parameter (string) is required")
	}

	trimmed := strings.TrimSpace(query)
	if !strings.HasPrefix(strings.ToUpper(trimmed), "SHOW") {
		query = "SHOW " + query
	}

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("mysql show: %w", err)
	}
	defer rows.Close()

	return scanRows(rows)
}

func (r *MySQLResource) execDescribe(ctx context.Context, params map[string]any) (any, error) {
	table, ok := params["table"].(string)
	if !ok || table == "" {
		return nil, fmt.Errorf("mysql describe: 'table' parameter (string) is required")
	}

	// Use backtick quoting for the table name to prevent injection.
	query := fmt.Sprintf("DESCRIBE `%s`", strings.ReplaceAll(table, "`", "``"))

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("mysql describe: %w", err)
	}
	defer rows.Close()

	return scanRows(rows)
}

func (r *MySQLResource) execExplain(ctx context.Context, params map[string]any) (any, error) {
	query, ok := params["query"].(string)
	if !ok || query == "" {
		return nil, fmt.Errorf("mysql explain: 'query' parameter (string) is required")
	}

	trimmed := strings.TrimSpace(query)
	if !strings.HasPrefix(strings.ToUpper(trimmed), "EXPLAIN") {
		query = "EXPLAIN " + query
	}

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("mysql explain: %w", err)
	}
	defer rows.Close()

	return scanRows(rows)
}

// scanRows converts sql.Rows into a slice of maps, where each map represents a row
// with column names as keys and their values as strings or nil.
func scanRows(rows *sql.Rows) ([]map[string]any, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("getting columns: %w", err)
	}

	var results []map[string]any

	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		row := make(map[string]any, len(columns))
		for i, col := range columns {
			val := values[i]
			// Convert byte slices to strings for readability.
			if b, ok := val.([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = val
			}
		}
		results = append(results, row)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rows: %w", err)
	}

	if results == nil {
		results = []map[string]any{}
	}
	return results, nil
}
