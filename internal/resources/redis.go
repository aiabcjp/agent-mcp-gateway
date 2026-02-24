package resources

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agent-gateway/internal/config"

	"github.com/redis/go-redis/v9"
)

// RedisResource implements the Resource interface for Redis backends.
type RedisResource struct {
	client     *redis.Client
	desc       string
	allowedOps []string
}

// newRedisResource creates a new RedisResource from the given configuration.
// The cfg.Host should be in "host:port" format.
// If dialFn is not nil, it is used as the custom dialer for the Redis client.
func newRedisResource(name string, cfg config.ResourceConfig, dialFn DialContextFunc) (*RedisResource, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("redis resource %q: host is required", name)
	}

	opts := &redis.Options{
		Addr:     cfg.Host,
		Password: cfg.Password,
	}

	if dialFn != nil {
		opts.Dialer = dialFn
	}

	client := redis.NewClient(opts)

	return &RedisResource{
		client:     client,
		desc:       cfg.Description,
		allowedOps: cfg.AllowedOps,
	}, nil
}

// Type returns the resource type identifier.
func (r *RedisResource) Type() string { return "redis" }

// Description returns the human-readable description of this resource.
func (r *RedisResource) Description() string { return r.desc }

// AllowedOps returns the list of allowed operations.
func (r *RedisResource) AllowedOps() []string { return r.allowedOps }

// Close closes the underlying Redis client connection.
func (r *RedisResource) Close() error {
	return r.client.Close()
}

// Execute runs the specified operation on the Redis backend.
// Supported ops (case-insensitive): get, set, keys, del, ttl, info, scan.
func (r *RedisResource) Execute(ctx context.Context, op string, params map[string]any) (any, error) {
	op = strings.ToLower(op)

	if !isOpAllowed(r.allowedOps, op) {
		return nil, fmt.Errorf("operation %q is not allowed on this resource", op)
	}

	switch op {
	case "get":
		return r.execGet(ctx, params)
	case "set":
		return r.execSet(ctx, params)
	case "keys":
		return r.execKeys(ctx, params)
	case "del":
		return r.execDel(ctx, params)
	case "ttl":
		return r.execTTL(ctx, params)
	case "info":
		return r.execInfo(ctx, params)
	case "scan":
		return r.execScan(ctx, params)
	default:
		return nil, fmt.Errorf("unsupported redis operation: %s", op)
	}
}

func (r *RedisResource) execGet(ctx context.Context, params map[string]any) (any, error) {
	key, ok := params["key"].(string)
	if !ok || key == "" {
		return nil, fmt.Errorf("redis get: 'key' parameter (string) is required")
	}
	val, err := r.client.Get(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("redis get: %w", err)
	}
	return val, nil
}

func (r *RedisResource) execSet(ctx context.Context, params map[string]any) (any, error) {
	key, ok := params["key"].(string)
	if !ok || key == "" {
		return nil, fmt.Errorf("redis set: 'key' parameter (string) is required")
	}
	value, ok := params["value"].(string)
	if !ok {
		return nil, fmt.Errorf("redis set: 'value' parameter (string) is required")
	}

	var expiration time.Duration
	if ttl, ok := params["ttl"].(float64); ok && ttl > 0 {
		expiration = time.Duration(ttl * float64(time.Second))
	}

	err := r.client.Set(ctx, key, value, expiration).Err()
	if err != nil {
		return nil, fmt.Errorf("redis set: %w", err)
	}
	return "OK", nil
}

func (r *RedisResource) execKeys(ctx context.Context, params map[string]any) (any, error) {
	pattern, ok := params["pattern"].(string)
	if !ok || pattern == "" {
		return nil, fmt.Errorf("redis keys: 'pattern' parameter (string) is required")
	}
	keys, err := r.client.Keys(ctx, pattern).Result()
	if err != nil {
		return nil, fmt.Errorf("redis keys: %w", err)
	}
	return keys, nil
}

func (r *RedisResource) execDel(ctx context.Context, params map[string]any) (any, error) {
	key, ok := params["key"].(string)
	if !ok || key == "" {
		return nil, fmt.Errorf("redis del: 'key' parameter (string) is required")
	}
	n, err := r.client.Del(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("redis del: %w", err)
	}
	return n, nil
}

func (r *RedisResource) execTTL(ctx context.Context, params map[string]any) (any, error) {
	key, ok := params["key"].(string)
	if !ok || key == "" {
		return nil, fmt.Errorf("redis ttl: 'key' parameter (string) is required")
	}
	dur, err := r.client.TTL(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("redis ttl: %w", err)
	}
	return dur.Seconds(), nil
}

func (r *RedisResource) execInfo(ctx context.Context, params map[string]any) (any, error) {
	var sections []string
	if section, ok := params["section"].(string); ok && section != "" {
		sections = append(sections, section)
	}
	info, err := r.client.Info(ctx, sections...).Result()
	if err != nil {
		return nil, fmt.Errorf("redis info: %w", err)
	}
	return info, nil
}

func (r *RedisResource) execScan(ctx context.Context, params map[string]any) (any, error) {
	pattern, ok := params["pattern"].(string)
	if !ok || pattern == "" {
		return nil, fmt.Errorf("redis scan: 'pattern' parameter (string) is required")
	}

	count := int64(100)
	if c, ok := params["count"].(float64); ok && c > 0 {
		count = int64(c)
	}

	var allKeys []string
	var cursor uint64
	for {
		keys, nextCursor, err := r.client.Scan(ctx, cursor, pattern, count).Result()
		if err != nil {
			return nil, fmt.Errorf("redis scan: %w", err)
		}
		allKeys = append(allKeys, keys...)
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return allKeys, nil
}
