package metering

import (
	"context"
	"testing"
	"time"
)

func TestNewSQLiteMeterCreatesTable(t *testing.T) {
	meter, err := NewSQLiteMeter(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteMeter: unexpected error: %v", err)
	}
	defer meter.Close()

	// Verify the table exists by querying it.
	m := meter.(*SQLiteMeter)
	var count int
	err = m.db.QueryRow("SELECT COUNT(*) FROM usage_log").Scan(&count)
	if err != nil {
		t.Fatalf("querying usage_log table: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 rows in new table, got %d", count)
	}
}

func TestRecordInsertsEntry(t *testing.T) {
	meter, err := NewSQLiteMeter(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteMeter: unexpected error: %v", err)
	}
	defer meter.Close()

	ctx := context.Background()
	entry := &UsageEntry{
		UserID:    "user1",
		Email:     "user1@example.com",
		Resource:  "redis-cache",
		Operation: "get",
		Bytes:     256,
		Latency:   50 * time.Millisecond,
		Timestamp: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
		Success:   true,
		Error:     "",
	}

	err = meter.Record(ctx, entry)
	if err != nil {
		t.Fatalf("Record: unexpected error: %v", err)
	}

	// Verify the entry was inserted.
	m := meter.(*SQLiteMeter)
	var (
		userID    string
		email     string
		resource  string
		operation string
		bytes     int64
		latencyMs int64
		success   bool
		errStr    string
	)
	err = m.db.QueryRow(`
		SELECT user_id, email, resource, operation, bytes, latency_ms, success, error
		FROM usage_log WHERE user_id = ?`, "user1").
		Scan(&userID, &email, &resource, &operation, &bytes, &latencyMs, &success, &errStr)
	if err != nil {
		t.Fatalf("querying inserted entry: %v", err)
	}

	if userID != "user1" {
		t.Errorf("expected user_id 'user1', got %q", userID)
	}
	if email != "user1@example.com" {
		t.Errorf("expected email 'user1@example.com', got %q", email)
	}
	if resource != "redis-cache" {
		t.Errorf("expected resource 'redis-cache', got %q", resource)
	}
	if operation != "get" {
		t.Errorf("expected operation 'get', got %q", operation)
	}
	if bytes != 256 {
		t.Errorf("expected bytes 256, got %d", bytes)
	}
	if latencyMs != 50 {
		t.Errorf("expected latency_ms 50, got %d", latencyMs)
	}
	if !success {
		t.Error("expected success to be true")
	}
	if errStr != "" {
		t.Errorf("expected empty error, got %q", errStr)
	}
}

func TestGetUsageReturnsCorrectStats(t *testing.T) {
	meter, err := NewSQLiteMeter(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteMeter: unexpected error: %v", err)
	}
	defer meter.Close()

	ctx := context.Background()
	baseTime := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)

	entries := []*UsageEntry{
		{
			UserID:    "user1",
			Email:     "user1@example.com",
			Resource:  "redis-cache",
			Operation: "get",
			Bytes:     100,
			Latency:   10 * time.Millisecond,
			Timestamp: baseTime,
			Success:   true,
		},
		{
			UserID:    "user1",
			Email:     "user1@example.com",
			Resource:  "redis-cache",
			Operation: "set",
			Bytes:     200,
			Latency:   20 * time.Millisecond,
			Timestamp: baseTime.Add(1 * time.Minute),
			Success:   true,
		},
		{
			UserID:    "user1",
			Email:     "user1@example.com",
			Resource:  "mysql-db",
			Operation: "select",
			Bytes:     500,
			Latency:   100 * time.Millisecond,
			Timestamp: baseTime.Add(2 * time.Minute),
			Success:   true,
		},
	}

	for _, e := range entries {
		if err := meter.Record(ctx, e); err != nil {
			t.Fatalf("Record: unexpected error: %v", err)
		}
	}

	from := baseTime.Add(-1 * time.Hour)
	to := baseTime.Add(1 * time.Hour)

	stats, err := meter.GetUsage(ctx, "user1", from, to)
	if err != nil {
		t.Fatalf("GetUsage: unexpected error: %v", err)
	}

	if stats.TotalRequests != 3 {
		t.Errorf("expected TotalRequests 3, got %d", stats.TotalRequests)
	}
	if stats.TotalBytes != 800 {
		t.Errorf("expected TotalBytes 800, got %d", stats.TotalBytes)
	}

	// Check by resource.
	if stats.ByResource["redis-cache"] != 2 {
		t.Errorf("expected 2 redis-cache requests, got %d", stats.ByResource["redis-cache"])
	}
	if stats.ByResource["mysql-db"] != 1 {
		t.Errorf("expected 1 mysql-db request, got %d", stats.ByResource["mysql-db"])
	}

	// Check by operation.
	if stats.ByOperation["get"] != 1 {
		t.Errorf("expected 1 get operation, got %d", stats.ByOperation["get"])
	}
	if stats.ByOperation["set"] != 1 {
		t.Errorf("expected 1 set operation, got %d", stats.ByOperation["set"])
	}
	if stats.ByOperation["select"] != 1 {
		t.Errorf("expected 1 select operation, got %d", stats.ByOperation["select"])
	}
}

func TestGetUsageWithTimeRangeFiltering(t *testing.T) {
	meter, err := NewSQLiteMeter(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteMeter: unexpected error: %v", err)
	}
	defer meter.Close()

	ctx := context.Background()

	// Insert entries at different times.
	entries := []*UsageEntry{
		{
			UserID:    "user1",
			Email:     "user1@example.com",
			Resource:  "redis",
			Operation: "get",
			Bytes:     100,
			Timestamp: time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC),
			Success:   true,
		},
		{
			UserID:    "user1",
			Email:     "user1@example.com",
			Resource:  "redis",
			Operation: "get",
			Bytes:     200,
			Timestamp: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			Success:   true,
		},
		{
			UserID:    "user1",
			Email:     "user1@example.com",
			Resource:  "redis",
			Operation: "get",
			Bytes:     300,
			Timestamp: time.Date(2025, 6, 30, 10, 0, 0, 0, time.UTC),
			Success:   true,
		},
	}

	for _, e := range entries {
		if err := meter.Record(ctx, e); err != nil {
			t.Fatalf("Record: unexpected error: %v", err)
		}
	}

	// Query only the middle time range.
	from := time.Date(2025, 6, 10, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 6, 20, 0, 0, 0, 0, time.UTC)

	stats, err := meter.GetUsage(ctx, "user1", from, to)
	if err != nil {
		t.Fatalf("GetUsage: unexpected error: %v", err)
	}

	if stats.TotalRequests != 1 {
		t.Errorf("expected TotalRequests 1 (only middle entry), got %d", stats.TotalRequests)
	}
	if stats.TotalBytes != 200 {
		t.Errorf("expected TotalBytes 200, got %d", stats.TotalBytes)
	}
}

func TestGetUsageNoMatchingEntries(t *testing.T) {
	meter, err := NewSQLiteMeter(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteMeter: unexpected error: %v", err)
	}
	defer meter.Close()

	ctx := context.Background()

	// Record an entry for a different user.
	entry := &UsageEntry{
		UserID:    "user2",
		Email:     "user2@example.com",
		Resource:  "redis",
		Operation: "get",
		Bytes:     100,
		Timestamp: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
		Success:   true,
	}
	if err := meter.Record(ctx, entry); err != nil {
		t.Fatalf("Record: unexpected error: %v", err)
	}

	// Query for a user that has no entries.
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)

	stats, err := meter.GetUsage(ctx, "user1", from, to)
	if err != nil {
		t.Fatalf("GetUsage: unexpected error: %v", err)
	}

	if stats.TotalRequests != 0 {
		t.Errorf("expected TotalRequests 0, got %d", stats.TotalRequests)
	}
	if stats.TotalBytes != 0 {
		t.Errorf("expected TotalBytes 0, got %d", stats.TotalBytes)
	}
	if len(stats.ByResource) != 0 {
		t.Errorf("expected empty ByResource, got %v", stats.ByResource)
	}
	if len(stats.ByOperation) != 0 {
		t.Errorf("expected empty ByOperation, got %v", stats.ByOperation)
	}
}

func TestGetUsageMultipleUsersIsolated(t *testing.T) {
	meter, err := NewSQLiteMeter(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteMeter: unexpected error: %v", err)
	}
	defer meter.Close()

	ctx := context.Background()
	baseTime := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)

	entries := []*UsageEntry{
		{
			UserID:    "user1",
			Email:     "user1@example.com",
			Resource:  "redis",
			Operation: "get",
			Bytes:     100,
			Timestamp: baseTime,
			Success:   true,
		},
		{
			UserID:    "user2",
			Email:     "user2@example.com",
			Resource:  "redis",
			Operation: "get",
			Bytes:     200,
			Timestamp: baseTime,
			Success:   true,
		},
		{
			UserID:    "user1",
			Email:     "user1@example.com",
			Resource:  "mysql",
			Operation: "select",
			Bytes:     300,
			Timestamp: baseTime.Add(1 * time.Minute),
			Success:   true,
		},
	}

	for _, e := range entries {
		if err := meter.Record(ctx, e); err != nil {
			t.Fatalf("Record: unexpected error: %v", err)
		}
	}

	from := baseTime.Add(-1 * time.Hour)
	to := baseTime.Add(1 * time.Hour)

	// Check user1 stats.
	stats1, err := meter.GetUsage(ctx, "user1", from, to)
	if err != nil {
		t.Fatalf("GetUsage user1: unexpected error: %v", err)
	}
	if stats1.TotalRequests != 2 {
		t.Errorf("user1: expected TotalRequests 2, got %d", stats1.TotalRequests)
	}
	if stats1.TotalBytes != 400 {
		t.Errorf("user1: expected TotalBytes 400, got %d", stats1.TotalBytes)
	}

	// Check user2 stats.
	stats2, err := meter.GetUsage(ctx, "user2", from, to)
	if err != nil {
		t.Fatalf("GetUsage user2: unexpected error: %v", err)
	}
	if stats2.TotalRequests != 1 {
		t.Errorf("user2: expected TotalRequests 1, got %d", stats2.TotalRequests)
	}
	if stats2.TotalBytes != 200 {
		t.Errorf("user2: expected TotalBytes 200, got %d", stats2.TotalBytes)
	}
}

func TestRecordWithError(t *testing.T) {
	meter, err := NewSQLiteMeter(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteMeter: unexpected error: %v", err)
	}
	defer meter.Close()

	ctx := context.Background()
	entry := &UsageEntry{
		UserID:    "user1",
		Email:     "user1@example.com",
		Resource:  "redis",
		Operation: "get",
		Bytes:     0,
		Latency:   5 * time.Millisecond,
		Timestamp: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
		Success:   false,
		Error:     "connection refused",
	}

	err = meter.Record(ctx, entry)
	if err != nil {
		t.Fatalf("Record: unexpected error: %v", err)
	}

	// Verify the error was stored.
	m := meter.(*SQLiteMeter)
	var errStr string
	var success bool
	err = m.db.QueryRow("SELECT success, error FROM usage_log WHERE user_id = ?", "user1").
		Scan(&success, &errStr)
	if err != nil {
		t.Fatalf("querying entry: %v", err)
	}
	if success {
		t.Error("expected success to be false")
	}
	if errStr != "connection refused" {
		t.Errorf("expected error 'connection refused', got %q", errStr)
	}
}

func TestClose(t *testing.T) {
	meter, err := NewSQLiteMeter(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteMeter: unexpected error: %v", err)
	}

	err = meter.Close()
	if err != nil {
		t.Fatalf("Close: unexpected error: %v", err)
	}

	// After closing, operations should fail.
	ctx := context.Background()
	entry := &UsageEntry{
		UserID:    "user1",
		Email:     "user1@example.com",
		Resource:  "redis",
		Operation: "get",
		Bytes:     100,
		Timestamp: time.Now(),
		Success:   true,
	}

	err = meter.Record(ctx, entry)
	if err == nil {
		t.Error("expected error after Close, got nil")
	}
}

func TestNewSQLiteMeterInvalidPath(t *testing.T) {
	// Try to open a database at an invalid path.
	_, err := NewSQLiteMeter("/nonexistent/directory/test.db")
	if err == nil {
		t.Fatal("expected error for invalid path, got nil")
	}
}
