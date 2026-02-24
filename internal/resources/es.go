package resources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"qa-mcp-gateway/internal/config"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

// ESResource implements the Resource interface for Elasticsearch backends.
type ESResource struct {
	client     *elasticsearch.Client
	desc       string
	allowedOps []string
}

// newESResource creates a new ESResource from the given configuration.
// The cfg.URL is used as the Elasticsearch address.
// If dialFn is not nil, a custom HTTP transport is created with the provided dialer.
func newESResource(name string, cfg config.ResourceConfig, dialFn DialContextFunc) (*ESResource, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("elasticsearch resource %q: URL is required", name)
	}

	esCfg := elasticsearch.Config{
		Addresses: []string{cfg.URL},
	}

	if dialFn != nil {
		esCfg.Transport = &http.Transport{
			DialContext: dialFn,
		}
	}

	client, err := elasticsearch.NewClient(esCfg)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch resource %q: creating client: %w", name, err)
	}

	return &ESResource{
		client:     client,
		desc:       cfg.Description,
		allowedOps: cfg.AllowedOps,
	}, nil
}

// Type returns the resource type identifier.
func (r *ESResource) Type() string { return "elasticsearch" }

// Description returns the human-readable description of this resource.
func (r *ESResource) Description() string { return r.desc }

// AllowedOps returns the list of allowed operations.
func (r *ESResource) AllowedOps() []string { return r.allowedOps }

// Close is a no-op for Elasticsearch as the client does not require explicit closing.
func (r *ESResource) Close() error {
	return nil
}

// Execute runs the specified operation on the Elasticsearch backend.
// Supported ops: search, cat, indices, count.
func (r *ESResource) Execute(ctx context.Context, op string, params map[string]any) (any, error) {
	op = strings.ToLower(op)

	if !isOpAllowed(r.allowedOps, op) {
		return nil, fmt.Errorf("operation %q is not allowed on this resource", op)
	}

	switch op {
	case "search":
		return r.execSearch(ctx, params)
	case "cat":
		return r.execCat(ctx, params)
	case "indices":
		return r.execIndices(ctx, params)
	case "count":
		return r.execCount(ctx, params)
	default:
		return nil, fmt.Errorf("unsupported elasticsearch operation: %s", op)
	}
}

func (r *ESResource) execSearch(ctx context.Context, params map[string]any) (any, error) {
	index, ok := params["index"].(string)
	if !ok || index == "" {
		return nil, fmt.Errorf("elasticsearch search: 'index' parameter (string) is required")
	}

	body, ok := params["body"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("elasticsearch search: 'body' parameter (object) is required")
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch search: marshaling body: %w", err)
	}

	res, err := r.client.Search(
		r.client.Search.WithContext(ctx),
		r.client.Search.WithIndex(index),
		r.client.Search.WithBody(bytes.NewReader(bodyBytes)),
	)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch search: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		bodyContent, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("elasticsearch search: status %s: %s", res.Status(), string(bodyContent))
	}

	var result map[string]any
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("elasticsearch search: decoding response: %w", err)
	}

	return result, nil
}

func (r *ESResource) execCat(ctx context.Context, params map[string]any) (any, error) {
	endpoint, ok := params["endpoint"].(string)
	if !ok || endpoint == "" {
		return nil, fmt.Errorf("elasticsearch cat: 'endpoint' parameter (string) is required")
	}

	// Use the low-level Perform method to call _cat endpoints.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/_cat/"+endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch cat: creating request: %w", err)
	}

	res, err := r.client.Perform(req)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch cat: %w", err)
	}
	defer res.Body.Close()

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch cat: reading response: %w", err)
	}

	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("elasticsearch cat: status %d: %s", res.StatusCode, string(bodyBytes))
	}

	return string(bodyBytes), nil
}

func (r *ESResource) execIndices(ctx context.Context, _ map[string]any) (any, error) {
	// Use _cat/indices with JSON format.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/_cat/indices?format=json", nil)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch indices: creating request: %w", err)
	}

	res, err := r.client.Perform(req)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch indices: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		bodyContent, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("elasticsearch indices: status %d: %s", res.StatusCode, string(bodyContent))
	}

	var result any
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("elasticsearch indices: decoding response: %w", err)
	}

	return result, nil
}

func (r *ESResource) execCount(ctx context.Context, params map[string]any) (any, error) {
	opts := []func(*esapi.CountRequest){
		r.client.Count.WithContext(ctx),
	}

	if index, ok := params["index"].(string); ok && index != "" {
		opts = append(opts, r.client.Count.WithIndex(index))
	}

	res, err := r.client.Count(opts...)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch count: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		bodyContent, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("elasticsearch count: status %s: %s", res.Status(), string(bodyContent))
	}

	var result map[string]any
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("elasticsearch count: decoding response: %w", err)
	}

	return result, nil
}
