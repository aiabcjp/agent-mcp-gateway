package resources

import (
	"context"
	"fmt"
	"net"
	"strings"

	"agent-mcp-gateway/internal/config"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// contextDialer wraps a DialContextFunc to implement the options.ContextDialer interface.
type contextDialer struct {
	fn DialContextFunc
}

// DialContext implements the options.ContextDialer interface.
func (d *contextDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d.fn(ctx, network, address)
}

// MongoResource implements the Resource interface for MongoDB backends.
type MongoResource struct {
	client     *mongo.Client
	db         *mongo.Database
	desc       string
	allowedOps []string
}

// newMongoResource creates a new MongoResource from the given configuration.
// The cfg.URI should be a valid MongoDB connection URI including the database name.
// If dialFn is not nil, it is used as the custom dialer for the MongoDB client.
func newMongoResource(name string, cfg config.ResourceConfig, dialFn DialContextFunc) (*MongoResource, error) {
	if cfg.URI == "" {
		return nil, fmt.Errorf("mongodb resource %q: URI is required", name)
	}

	opts := options.Client().ApplyURI(cfg.URI)

	if dialFn != nil {
		opts.SetDialer(&contextDialer{fn: dialFn})
	}

	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, fmt.Errorf("mongodb resource %q: failed to connect: %w", name, err)
	}

	// Extract the database name from the URI.
	dbName := extractDBName(cfg.URI)
	if dbName == "" {
		return nil, fmt.Errorf("mongodb resource %q: could not extract database name from URI", name)
	}

	return &MongoResource{
		client:     client,
		db:         client.Database(dbName),
		desc:       cfg.Description,
		allowedOps: cfg.AllowedOps,
	}, nil
}

// extractDBName extracts the database name from a MongoDB URI.
// It expects a format like: mongodb://host:port/dbname or mongodb://host:port/dbname?options
func extractDBName(uri string) string {
	// Remove the scheme prefix.
	trimmed := uri
	if idx := strings.Index(trimmed, "://"); idx >= 0 {
		trimmed = trimmed[idx+3:]
	}

	// Find the first slash after the host portion.
	slashIdx := strings.Index(trimmed, "/")
	if slashIdx < 0 {
		return ""
	}

	dbPart := trimmed[slashIdx+1:]

	// Remove query parameters.
	if qIdx := strings.Index(dbPart, "?"); qIdx >= 0 {
		dbPart = dbPart[:qIdx]
	}

	return dbPart
}

// Type returns the resource type identifier.
func (r *MongoResource) Type() string { return "mongodb" }

// Description returns the human-readable description of this resource.
func (r *MongoResource) Description() string { return r.desc }

// AllowedOps returns the list of allowed operations.
func (r *MongoResource) AllowedOps() []string { return r.allowedOps }

// Close disconnects the MongoDB client.
func (r *MongoResource) Close() error {
	if r.client != nil {
		return r.client.Disconnect(context.Background())
	}
	return nil
}

// Execute runs the specified operation on the MongoDB backend.
// Supported ops: find, aggregate, count, listCollections, distinct.
func (r *MongoResource) Execute(ctx context.Context, op string, params map[string]any) (any, error) {
	op = strings.ToLower(op)

	if !isOpAllowed(r.allowedOps, op) {
		return nil, fmt.Errorf("operation %q is not allowed on this resource", op)
	}

	switch op {
	case "find":
		return r.execFind(ctx, params)
	case "aggregate":
		return r.execAggregate(ctx, params)
	case "count":
		return r.execCount(ctx, params)
	case "listcollections":
		return r.execListCollections(ctx, params)
	case "distinct":
		return r.execDistinct(ctx, params)
	default:
		return nil, fmt.Errorf("unsupported mongodb operation: %s", op)
	}
}

func (r *MongoResource) execFind(ctx context.Context, params map[string]any) (any, error) {
	collName, ok := params["collection"].(string)
	if !ok || collName == "" {
		return nil, fmt.Errorf("mongodb find: 'collection' parameter (string) is required")
	}

	filter := bson.M{}
	if f, ok := params["filter"].(map[string]any); ok {
		filter = bson.M(f)
	}

	limit := int64(100)
	if l, ok := params["limit"].(float64); ok && l > 0 {
		limit = int64(l)
	}

	coll := r.db.Collection(collName)
	findOpts := options.Find().SetLimit(limit)

	cursor, err := coll.Find(ctx, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("mongodb find: %w", err)
	}
	defer cursor.Close(ctx)

	var results []bson.M
	if err := cursor.All(ctx, &results); err != nil {
		return nil, fmt.Errorf("mongodb find: decoding results: %w", err)
	}

	if results == nil {
		results = []bson.M{}
	}
	return results, nil
}

func (r *MongoResource) execAggregate(ctx context.Context, params map[string]any) (any, error) {
	collName, ok := params["collection"].(string)
	if !ok || collName == "" {
		return nil, fmt.Errorf("mongodb aggregate: 'collection' parameter (string) is required")
	}

	pipeline, ok := params["pipeline"].([]any)
	if !ok {
		return nil, fmt.Errorf("mongodb aggregate: 'pipeline' parameter (array) is required")
	}

	coll := r.db.Collection(collName)
	cursor, err := coll.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("mongodb aggregate: %w", err)
	}
	defer cursor.Close(ctx)

	var results []bson.M
	if err := cursor.All(ctx, &results); err != nil {
		return nil, fmt.Errorf("mongodb aggregate: decoding results: %w", err)
	}

	if results == nil {
		results = []bson.M{}
	}
	return results, nil
}

func (r *MongoResource) execCount(ctx context.Context, params map[string]any) (any, error) {
	collName, ok := params["collection"].(string)
	if !ok || collName == "" {
		return nil, fmt.Errorf("mongodb count: 'collection' parameter (string) is required")
	}

	filter := bson.M{}
	if f, ok := params["filter"].(map[string]any); ok {
		filter = bson.M(f)
	}

	coll := r.db.Collection(collName)
	count, err := coll.CountDocuments(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("mongodb count: %w", err)
	}

	return count, nil
}

func (r *MongoResource) execListCollections(ctx context.Context, _ map[string]any) (any, error) {
	names, err := r.db.ListCollectionNames(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("mongodb listCollections: %w", err)
	}
	if names == nil {
		names = []string{}
	}
	return names, nil
}

func (r *MongoResource) execDistinct(ctx context.Context, params map[string]any) (any, error) {
	collName, ok := params["collection"].(string)
	if !ok || collName == "" {
		return nil, fmt.Errorf("mongodb distinct: 'collection' parameter (string) is required")
	}

	field, ok := params["field"].(string)
	if !ok || field == "" {
		return nil, fmt.Errorf("mongodb distinct: 'field' parameter (string) is required")
	}

	filter := bson.M{}
	if f, ok := params["filter"].(map[string]any); ok {
		filter = bson.M(f)
	}

	coll := r.db.Collection(collName)
	result := coll.Distinct(ctx, field, filter)
	if result.Err() != nil {
		return nil, fmt.Errorf("mongodb distinct: %w", result.Err())
	}

	var values []any
	if err := result.Decode(&values); err != nil {
		return nil, fmt.Errorf("mongodb distinct: decoding results: %w", err)
	}

	if values == nil {
		values = []any{}
	}
	return values, nil
}
