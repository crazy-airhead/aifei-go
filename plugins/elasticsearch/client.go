package elasticsearch

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/crazy-airhead/aifei-go/log"
	elasticsearch8 "github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

// Client is an Elasticsearch client bound to one cluster.
//
// For advanced needs not covered by the high-level API (scrolling, reindex,
// aggregations, cluster admin), ESClient returns the underlying
// *elasticsearch.Client.
type Client interface {
	// Name returns the cluster/instance name.
	Name() string

	// Search executes a search query against index. The query is marshalled to
	// JSON; for raw JSON bodies use SearchRaw.
	Search(ctx context.Context, index string, query map[string]any) (*SearchResult, error)

	// SearchRaw executes a search with a raw JSON body (io.Reader).
	SearchRaw(ctx context.Context, index string, body io.Reader) (*SearchResult, error)

	// Index creates or replaces a document. An empty id lets ES auto-generate one.
	Index(ctx context.Context, index, id string, doc any) (*IndexResult, error)

	// Get retrieves a document by its _id.
	Get(ctx context.Context, index, id string) (*GetResult, error)

	// Delete removes a document by its _id. Deleting a missing document is not an
	// error — check DeleteResult.Result for "not_found".
	Delete(ctx context.Context, index, id string) (*DeleteResult, error)

	// BulkIndex indexes a batch of documents. It uses the bulk API; partial
	// failures are collected in BulkResult.Errors.
	BulkIndex(ctx context.Context, index string, docs []BulkDoc) (*BulkResult, error)

	// IndicesExists reports whether an index exists.
	IndicesExists(ctx context.Context, index string) (bool, error)

	// IndicesCreate creates an index with optional mappings.
	IndicesCreate(ctx context.Context, index string, mappings map[string]any) error

	// IndicesDelete deletes an index. Deleting a missing index is not an error.
	IndicesDelete(ctx context.Context, index string) error

	// Ping checks whether the cluster is reachable.
	Ping(ctx context.Context) error

	// Close releases any resources held by the client (idempotent).
	Close() error

	// ESClient returns the underlying go-elasticsearch client for advanced use.
	// Callers that use it become coupled to go-elasticsearch.
	ESClient() *elasticsearch8.Client
}

// SearchResult wraps an Elasticsearch search response body.
type SearchResult struct {
	Took     int            `json:"took"`
	TimedOut bool           `json:"timed_out"`
	Hits     *SearchHits    `json:"hits"`
	Raw      map[string]any `json:"-"`
}

// SearchHits wraps the hits portion of a search response.
type SearchHits struct {
	Total    *TotalHits      `json:"total"`
	MaxScore *float64        `json:"max_score"`
	Hits     []SearchHitItem `json:"hits"`
}

// TotalHits carries the total count and its relation ("eq" or "gte").
type TotalHits struct {
	Value    int64  `json:"value"`
	Relation string `json:"relation"`
}

// SearchHitItem is a single hit from a search response.
type SearchHitItem struct {
	Index  string         `json:"_index"`
	ID     string         `json:"_id"`
	Score  *float64       `json:"_score"`
	Source map[string]any `json:"_source"`
}

// IndexResult wraps an index/update response.
type IndexResult struct {
	Index   string `json:"_index"`
	ID      string `json:"_id"`
	Version int64  `json:"_version"`
	Result  string `json:"result"` // "created", "updated", etc.
}

// GetResult wraps a get-by-id response.
type GetResult struct {
	Index   string         `json:"_index"`
	ID      string         `json:"_id"`
	Version int64          `json:"_version"`
	Found   bool           `json:"found"`
	Source  map[string]any `json:"_source"`
}

// DeleteResult wraps a delete response.
type DeleteResult struct {
	Index   string `json:"_index"`
	ID      string `json:"_id"`
	Version int64  `json:"_version"`
	Result  string `json:"result"` // "deleted", "not_found"
}

// BulkDoc is a document for bulk indexing.
type BulkDoc struct {
	ID  string
	Doc any
}

// BulkResult wraps a bulk response.
type BulkResult struct {
	Errors bool             `json:"errors"`
	Items  []map[string]any `json:"items"`
	Took   int              `json:"took"`
}

// esClient implements Client, wrapping a go-elasticsearch *elasticsearch8.Client.
type esClient struct {
	name string
	cfg  ClusterConfig
	cl   *elasticsearch8.Client
	log  log.Logger
}

// newClient builds an Elasticsearch Client for one cluster from its configuration.
// Because go-elasticsearch dials lazily, this does not connect until the first
// request.
func newClient(name string, cfg ClusterConfig, logger log.Logger) (*esClient, error) {
	if logger == nil {
		logger = log.Default()
	}
	esCfg := elasticsearch8.Config{Addresses: cfg.Addresses}
	if cfg.Username != "" {
		esCfg.Username = cfg.Username
		esCfg.Password = cfg.Password
	}
	if cfg.APIKey != "" {
		esCfg.APIKey = cfg.APIKey
	}
	if cfg.CACert != "" || cfg.InsecureSkipVerify {
		tlsc, err := buildTLSConfig(cfg.CACert, cfg.InsecureSkipVerify)
		if err != nil {
			return nil, fmt.Errorf("elasticsearch: cluster %q: %w", name, err)
		}
		esCfg.Transport = &http.Transport{TLSClientConfig: tlsc}
	}
	cl, err := elasticsearch8.NewClient(esCfg)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: cluster %q: %w", name, err)
	}
	return &esClient{name: name, cfg: cfg, cl: cl, log: logger}, nil
}

// Name implements Client.
func (c *esClient) Name() string { return c.name }

// Search implements Client.
func (c *esClient) Search(ctx context.Context, index string, query map[string]any) (*SearchResult, error) {
	body, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: marshal query: %w", err)
	}
	return c.doSearch(ctx, index, bytes.NewReader(body))
}

// SearchRaw implements Client.
func (c *esClient) SearchRaw(ctx context.Context, index string, body io.Reader) (*SearchResult, error) {
	return c.doSearch(ctx, index, body)
}

func (c *esClient) doSearch(ctx context.Context, index string, body io.Reader) (*SearchResult, error) {
	opts := []func(*esapi.SearchRequest){
		c.cl.Search.WithContext(ctx),
		c.cl.Search.WithBody(body),
	}
	if index != "" {
		opts = append(opts, c.cl.Search.WithIndex(index))
	}
	resp, err := c.cl.Search(opts...)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: search %q: %w", c.name, err)
	}
	defer resp.Body.Close()
	if resp.IsError() {
		return nil, parseError(resp)
	}
	var result SearchResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("elasticsearch: decode search response: %w", err)
	}
	return &result, nil
}

// Index implements Client.
func (c *esClient) Index(ctx context.Context, index, id string, doc any) (*IndexResult, error) {
	body, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: marshal doc: %w", err)
	}
	req := esapi.IndexRequest{
		Index:      index,
		DocumentID: id,
		Body:       bytes.NewReader(body),
	}
	resp, err := req.Do(ctx, c.cl)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: index %q: %w", c.name, err)
	}
	defer resp.Body.Close()
	if resp.IsError() {
		return nil, parseError(resp)
	}
	var result IndexResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("elasticsearch: decode index response: %w", err)
	}
	return &result, nil
}

// Get implements Client.
func (c *esClient) Get(ctx context.Context, index, id string) (*GetResult, error) {
	req := esapi.GetRequest{Index: index, DocumentID: id}
	resp, err := req.Do(ctx, c.cl)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: get %q: %w", c.name, err)
	}
	defer resp.Body.Close()
	if resp.IsError() && resp.StatusCode != http.StatusNotFound {
		return nil, parseError(resp)
	}
	var result GetResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("elasticsearch: decode get response: %w", err)
	}
	return &result, nil
}

// Delete implements Client.
func (c *esClient) Delete(ctx context.Context, index, id string) (*DeleteResult, error) {
	req := esapi.DeleteRequest{Index: index, DocumentID: id}
	resp, err := req.Do(ctx, c.cl)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: delete %q: %w", c.name, err)
	}
	defer resp.Body.Close()
	if resp.IsError() && resp.StatusCode != http.StatusNotFound {
		return nil, parseError(resp)
	}
	var result DeleteResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("elasticsearch: decode delete response: %w", err)
	}
	return &result, nil
}

// BulkIndex implements Client.
func (c *esClient) BulkIndex(ctx context.Context, index string, docs []BulkDoc) (*BulkResult, error) {
	if len(docs) == 0 {
		return &BulkResult{}, nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, d := range docs {
		action := map[string]any{"index": map[string]any{"_index": index}}
		if d.ID != "" {
			action["index"].(map[string]any)["_id"] = d.ID
		}
		if err := enc.Encode(action); err != nil {
			return nil, fmt.Errorf("elasticsearch: encode bulk action: %w", err)
		}
		if err := enc.Encode(d.Doc); err != nil {
			return nil, fmt.Errorf("elasticsearch: encode bulk doc: %w", err)
		}
	}
	req := esapi.BulkRequest{Body: &buf}
	resp, err := req.Do(ctx, c.cl)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: bulk %q: %w", c.name, err)
	}
	defer resp.Body.Close()
	if resp.IsError() {
		return nil, parseError(resp)
	}
	var result BulkResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("elasticsearch: decode bulk response: %w", err)
	}
	return &result, nil
}

// IndicesExists implements Client.
func (c *esClient) IndicesExists(ctx context.Context, index string) (bool, error) {
	req := esapi.IndicesExistsRequest{Index: []string{index}}
	resp, err := req.Do(ctx, c.cl)
	if err != nil {
		return false, fmt.Errorf("elasticsearch: indices exists %q: %w", c.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return true, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	return false, parseError(resp)
}

// IndicesCreate implements Client.
func (c *esClient) IndicesCreate(ctx context.Context, index string, mappings map[string]any) error {
	req := esapi.IndicesCreateRequest{Index: index}
	if mappings != nil {
		body, err := json.Marshal(map[string]any{"mappings": mappings})
		if err != nil {
			return fmt.Errorf("elasticsearch: marshal mappings: %w", err)
		}
		req.Body = bytes.NewReader(body)
	}
	resp, err := req.Do(ctx, c.cl)
	if err != nil {
		return fmt.Errorf("elasticsearch: indices create %q: %w", c.name, err)
	}
	defer resp.Body.Close()
	if resp.IsError() {
		return parseError(resp)
	}
	return nil
}

// IndicesDelete implements Client.
func (c *esClient) IndicesDelete(ctx context.Context, index string) error {
	req := esapi.IndicesDeleteRequest{Index: []string{index}}
	resp, err := req.Do(ctx, c.cl)
	if err != nil {
		return fmt.Errorf("elasticsearch: indices delete %q: %w", c.name, err)
	}
	defer resp.Body.Close()
	if resp.IsError() && resp.StatusCode != http.StatusNotFound {
		return parseError(resp)
	}
	return nil
}

// Ping implements Client.
func (c *esClient) Ping(ctx context.Context) error {
	req := esapi.PingRequest{}
	resp, err := req.Do(ctx, c.cl)
	if err != nil {
		return fmt.Errorf("elasticsearch: ping %q: %w", c.name, err)
	}
	defer resp.Body.Close()
	if resp.IsError() {
		return parseError(resp)
	}
	return nil
}

// Close implements Client.
func (c *esClient) Close() error {
	// go-elasticsearch v8's Client has no explicit Close; connections are
	// managed by the transport's idle-connection pool.
	return nil
}

// ESClient implements Client.
func (c *esClient) ESClient() *elasticsearch8.Client { return c.cl }

// ---- helpers ----

// buildTLSConfig builds a *tls.Config from a CA cert file path, mirroring the
// kafka plugin's TLS setup.
func buildTLSConfig(caCert string, insecure bool) (*tls.Config, error) {
	tlsc := &tls.Config{InsecureSkipVerify: insecure}
	if caCert != "" {
		pem, err := os.ReadFile(caCert)
		if err != nil {
			return nil, fmt.Errorf("elasticsearch: read ca cert %q: %w", caCert, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("elasticsearch: no certificates parsed from ca cert %q", caCert)
		}
		tlsc.RootCAs = pool
	}
	return tlsc, nil
}

// parseError reads an ES error response body and returns it as a Go error.
func parseError(resp *esapi.Response) error {
	defer resp.Body.Close()
	var e map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		// If we can't decode the error body, report the bare status.
		return fmt.Errorf("elasticsearch: %s", resp.Status())
	}
	errInfo := e["error"]
	msg := errInfo
	if m, ok := errInfo.(map[string]any); ok {
		if reason, ok := m["reason"]; ok {
			msg = reason
		}
	}
	return fmt.Errorf("elasticsearch: %s: %v (%s)", resp.Status(), msg, strings.TrimSpace(resp.String()))
}
