// Package metering provides usage tracking and reporting for gateway operations.
package metering

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // CGO-free SQLite driver, registered as "sqlite".
)

// UsageEntry represents a single usage event to be recorded.
type UsageEntry struct {
	UserID    string
	Email     string
	Resource  string
	Operation string
	Bytes     int64
	Latency   time.Duration
	Timestamp time.Time
	Success   bool
	Error     string
}

// UsageStats contains aggregated usage statistics.
type UsageStats struct {
	TotalRequests int64            `json:"total_requests"`
	TotalBytes    int64            `json:"total_bytes"`
	ByResource    map[string]int64 `json:"by_resource"`
	ByOperation   map[string]int64 `json:"by_operation"`
}

// Meter defines the interface for recording and querying usage data.
type Meter interface {
	Record(ctx context.Context, entry *UsageEntry) error
	GetUsage(ctx context.Context, userID string, from, to time.Time) (*UsageStats, error)
	Close() error
}

// SQLiteMeter implements the Meter interface using a SQLite database.
type SQLiteMeter struct {
	db *sql.DB
}

// NewSQLiteMeter opens a SQLite database at the given path and creates the
// usage_log table if it does not exist. Use ":memory:" for an in-memory database.
func NewSQLiteMeter(dbPath string) (Meter, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite database: %w", err)
	}

	// Create the usage_log table if it does not exist.
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS usage_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			email TEXT NOT NULL,
			resource TEXT NOT NULL,
			operation TEXT NOT NULL,
			bytes INTEGER DEFAULT 0,
			latency_ms INTEGER DEFAULT 0,
			timestamp DATETIME NOT NULL,
			success BOOLEAN NOT NULL,
			error TEXT DEFAULT ''
		)`

	if _, err := db.Exec(createTableSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating usage_log table: %w", err)
	}

	createIndexSQL := `CREATE INDEX IF NOT EXISTS idx_usage_user_time ON usage_log(user_id, timestamp)`
	if _, err := db.Exec(createIndexSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating index: %w", err)
	}

	return &SQLiteMeter{db: db}, nil
}

// Record inserts a usage entry into the database.
func (m *SQLiteMeter) Record(ctx context.Context, entry *UsageEntry) error {
	insertSQL := `
		INSERT INTO usage_log (user_id, email, resource, operation, bytes, latency_ms, timestamp, success, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	latencyMs := entry.Latency.Milliseconds()

	_, err := m.db.ExecContext(ctx, insertSQL,
		entry.UserID,
		entry.Email,
		entry.Resource,
		entry.Operation,
		entry.Bytes,
		latencyMs,
		entry.Timestamp.UTC().Format(time.RFC3339),
		entry.Success,
		entry.Error,
	)
	if err != nil {
		return fmt.Errorf("recording usage entry: %w", err)
	}

	return nil
}

// GetUsage retrieves aggregated usage statistics for the given user within the
// specified time range.
func (m *SQLiteMeter) GetUsage(ctx context.Context, userID string, from, to time.Time) (*UsageStats, error) {
	stats := &UsageStats{
		ByResource:  make(map[string]int64),
		ByOperation: make(map[string]int64),
	}

	fromStr := from.UTC().Format(time.RFC3339)
	toStr := to.UTC().Format(time.RFC3339)

	// Get total requests and total bytes.
	totalsSQL := `
		SELECT COUNT(*), COALESCE(SUM(bytes), 0)
		FROM usage_log
		WHERE user_id = ? AND timestamp >= ? AND timestamp <= ?`

	err := m.db.QueryRowContext(ctx, totalsSQL, userID, fromStr, toStr).
		Scan(&stats.TotalRequests, &stats.TotalBytes)
	if err != nil {
		return nil, fmt.Errorf("querying usage totals: %w", err)
	}

	// Get counts by resource.
	byResourceSQL := `
		SELECT resource, COUNT(*)
		FROM usage_log
		WHERE user_id = ? AND timestamp >= ? AND timestamp <= ?
		GROUP BY resource`

	rows, err := m.db.QueryContext(ctx, byResourceSQL, userID, fromStr, toStr)
	if err != nil {
		return nil, fmt.Errorf("querying usage by resource: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var resource string
		var count int64
		if err := rows.Scan(&resource, &count); err != nil {
			return nil, fmt.Errorf("scanning resource row: %w", err)
		}
		stats.ByResource[resource] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating resource rows: %w", err)
	}

	// Get counts by operation.
	byOperationSQL := `
		SELECT operation, COUNT(*)
		FROM usage_log
		WHERE user_id = ? AND timestamp >= ? AND timestamp <= ?
		GROUP BY operation`

	rows2, err := m.db.QueryContext(ctx, byOperationSQL, userID, fromStr, toStr)
	if err != nil {
		return nil, fmt.Errorf("querying usage by operation: %w", err)
	}
	defer rows2.Close()

	for rows2.Next() {
		var operation string
		var count int64
		if err := rows2.Scan(&operation, &count); err != nil {
			return nil, fmt.Errorf("scanning operation row: %w", err)
		}
		stats.ByOperation[operation] = count
	}
	if err := rows2.Err(); err != nil {
		return nil, fmt.Errorf("iterating operation rows: %w", err)
	}

	return stats, nil
}

// Close closes the underlying database connection.
func (m *SQLiteMeter) Close() error {
	if m.db != nil {
		return m.db.Close()
	}
	return nil
}
