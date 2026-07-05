// Package opensearch is an OpenSearch kNN (k-nearest-neighbors) plugin
// backend for oss.nandlabs.io/golly/vectorstore.
//
// It talks to any OpenSearch cluster whose index has been configured with the
// kNN plugin — see the README for the mapping the Store expects. The Store
// does NOT manage the target index by default: callers create it out of band
// (or via the CreateIndex helper) and pass a ready *opensearch.Client.
//
// Target scale profile: managed OpenSearch clusters (Amazon OpenSearch
// Service, OpenSearch Serverless, self-hosted) at 10^5 – 10^7 vectors per
// index, with HNSW ANN via the nmslib or faiss engine and JSON-side metadata
// filtering.
//
// The Store is safe for concurrent use.
package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"

	"github.com/opensearch-project/opensearch-go/v3"
	"github.com/opensearch-project/opensearch-go/v3/opensearchapi"

	"oss.nandlabs.io/golly/vectorstore"
)

// defaultBulkBatchSize caps the number of documents sent in one _bulk call.
// OpenSearch accepts larger batches, but 500 is a common sweet spot that
// keeps request bodies well under typical 100 MB HTTP limits.
const defaultBulkBatchSize = 500

// Distance spaces supported by the OpenSearch kNN plugin. Values are the
// wire-level strings expected in the index mapping.
const (
	SpaceCosine = "cosinesimil"
	SpaceL2     = "l2"
	SpaceIP     = "innerproduct"
)

// Default kNN engine used when Options.KnnEngine is empty. nmslib is the
// OpenSearch default and supports every distance space above.
const defaultEngine = "nmslib"

// Options configures a Store.
type Options struct {
	// Client is a caller-owned *opensearch.Client. The Store never closes
	// the client; the caller is responsible for its lifetime.
	Client *opensearch.Client
	// Index is the target index name (e.g. "docs"). Required.
	Index string
	// Dimension is the embedding dimension. Only used by CreateIndex — the
	// Store itself does not validate vector dimensions at write time.
	Dimension int
	// Space selects the kNN distance metric written into the index mapping
	// when CreateIndex is used. One of SpaceCosine, SpaceL2, SpaceIP.
	// Defaults to SpaceCosine.
	Space string
	// KnnEngine is the underlying ANN engine ("nmslib", "faiss", ...).
	// Empty uses the OpenSearch default (nmslib).
	KnnEngine string
	// BulkBatchSize overrides the default batch cap for Upsert / Delete.
	// Zero uses the package default (500).
	BulkBatchSize int
}

// Store implements vectorstore.Store on top of an OpenSearch index with the
// kNN plugin enabled.
type Store struct {
	client    *opensearch.Client
	index     string
	dimension int
	space     string
	engine    string
	batchSize int
	closed    atomic.Bool
}

// New constructs a Store from Options. It does NOT create the target index —
// call CreateIndex if you need bootstrap behavior.
func New(opts Options) (*Store, error) {
	if opts.Client == nil {
		return nil, errors.New("vectorstore/opensearch: Options.Client is required")
	}
	if opts.Index == "" {
		return nil, errors.New("vectorstore/opensearch: Options.Index is required")
	}
	space := opts.Space
	if space == "" {
		space = SpaceCosine
	}
	switch space {
	case SpaceCosine, SpaceL2, SpaceIP:
	default:
		return nil, fmt.Errorf("vectorstore/opensearch: unknown Space %q", space)
	}
	engine := opts.KnnEngine
	if engine == "" {
		engine = defaultEngine
	}
	batch := opts.BulkBatchSize
	if batch <= 0 {
		batch = defaultBulkBatchSize
	}
	return &Store{
		client:    opts.Client,
		index:     opts.Index,
		dimension: opts.Dimension,
		space:     space,
		engine:    engine,
		batchSize: batch,
	}, nil
}

// CreateIndex is a bootstrap helper: it issues a PUT /<index> with a mapping
// that declares an `embedding` knn_vector field and a `metadata` object.
// Callers who manage their own index schema (Terraform, migrations, ...)
// should skip it.
//
// The mapping the helper writes is:
//
//	{
//	  "settings": {"index.knn": true},
//	  "mappings": {
//	    "properties": {
//	      "embedding": {"type":"knn_vector","dimension":<Dimension>,
//	                    "method":{"name":"hnsw","space_type":"<Space>","engine":"<Engine>"}},
//	      "content":   {"type":"text"},
//	      "metadata":  {"type":"object"}
//	    }
//	  }
//	}
func (s *Store) CreateIndex(ctx context.Context) error {
	if s.closed.Load() {
		return errClosed
	}
	if s.dimension <= 0 {
		return errors.New("vectorstore/opensearch: CreateIndex requires Options.Dimension > 0")
	}
	body := map[string]any{
		"settings": map[string]any{"index.knn": true},
		"mappings": map[string]any{
			"properties": map[string]any{
				"embedding": map[string]any{
					"type":      "knn_vector",
					"dimension": s.dimension,
					"method": map[string]any{
						"name":       "hnsw",
						"space_type": s.space,
						"engine":     s.engine,
					},
				},
				"content":  map[string]any{"type": "text"},
				"metadata": map[string]any{"type": "object"},
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("vectorstore/opensearch: marshal create-index body: %w", err)
	}
	req := opensearchapi.IndicesCreateReq{
		Index: s.index,
		Body:  bytes.NewReader(raw),
	}
	if _, err := s.do(ctx, req, nil); err != nil {
		return fmt.Errorf("vectorstore/opensearch: create index %q: %w", s.index, err)
	}
	return nil
}

// Upsert inserts or replaces docs by ID via a single (or batched) _bulk call.
// Batches larger than BulkBatchSize are split across multiple RPCs; a failure
// on any batch aborts the sequence and later docs are not written.
func (s *Store) Upsert(ctx context.Context, docs ...vectorstore.Doc) error {
	if s.closed.Load() {
		return errClosed
	}
	if len(docs) == 0 {
		return nil
	}
	for _, d := range docs {
		if err := vectorstore.ValidateDoc(d); err != nil {
			return err
		}
	}
	for start := 0; start < len(docs); start += s.batchSize {
		end := start + s.batchSize
		if end > len(docs) {
			end = len(docs)
		}
		body, err := buildIndexBulkBody(s.index, docs[start:end])
		if err != nil {
			return err
		}
		if err := s.bulk(ctx, body, "upsert"); err != nil {
			return err
		}
	}
	return nil
}

// Search runs a kNN query against the index and returns hits ordered by
// descending score (OpenSearch already returns them that way; we forward
// the ordering untouched).
//
// Query.Filter is translated into a `bool.must` list of `term` clauses on
// `metadata.<key>` — matching the memory backend's equality-per-key
// semantics. Nested boolean expressions are not supported; callers who
// need them should embed a raw OpenSearch query object under a special key
// (`_raw_query`) instead. (Reserved but not yet implemented.)
func (s *Store) Search(ctx context.Context, q vectorstore.Query) ([]vectorstore.Hit, error) {
	if s.closed.Load() {
		return nil, errClosed
	}
	if len(q.Vector) == 0 {
		return nil, vectorstore.ErrEmptyVector
	}
	topK := q.TopK
	if topK <= 0 {
		topK = 10
	}

	knn := map[string]any{
		"vector": []float32(q.Vector),
		"k":      topK,
	}

	var query any
	if len(q.Filter) > 0 {
		mustClauses := make([]map[string]any, 0, len(q.Filter))
		// Deterministic order — sorted keys — so equality checks in tests
		// don't have to tolerate map-iteration randomness.
		for _, k := range sortedKeys(q.Filter) {
			mustClauses = append(mustClauses, map[string]any{
				"term": map[string]any{"metadata." + k: q.Filter[k]},
			})
		}
		knn["filter"] = map[string]any{"bool": map[string]any{"must": mustClauses}}
		query = map[string]any{"knn": map[string]any{"embedding": knn}}
	} else {
		query = map[string]any{"knn": map[string]any{"embedding": knn}}
	}

	body := map[string]any{
		"size":  topK,
		"query": query,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("vectorstore/opensearch: marshal search body: %w", err)
	}

	req := opensearchapi.SearchReq{
		Indices: []string{s.index},
		Body:    bytes.NewReader(raw),
	}
	var resp opensearchapi.SearchResp
	if _, err := s.do(ctx, req, &resp); err != nil {
		return nil, fmt.Errorf("vectorstore/opensearch: search %q: %w", s.index, err)
	}
	hits := make([]vectorstore.Hit, 0, len(resp.Hits.Hits))
	for _, h := range resp.Hits.Hits {
		if q.MinScore != 0 && h.Score < q.MinScore {
			continue
		}
		var src searchSource
		if len(h.Source) > 0 {
			if err := json.Unmarshal(h.Source, &src); err != nil {
				return nil, fmt.Errorf("vectorstore/opensearch: parse _source for %q: %w", h.ID, err)
			}
		}
		hits = append(hits, vectorstore.Hit{
			ID:       h.ID,
			Score:    h.Score,
			Metadata: src.Metadata,
			Content:  src.Content,
		})
	}
	return hits, nil
}

// Delete removes docs by id via _bulk with delete actions. Batches larger
// than BulkBatchSize split across multiple calls.
func (s *Store) Delete(ctx context.Context, ids ...string) error {
	if s.closed.Load() {
		return errClosed
	}
	if len(ids) == 0 {
		return nil
	}
	for start := 0; start < len(ids); start += s.batchSize {
		end := start + s.batchSize
		if end > len(ids) {
			end = len(ids)
		}
		body := buildDeleteBulkBody(s.index, ids[start:end])
		if err := s.bulk(ctx, body, "delete"); err != nil {
			return err
		}
	}
	return nil
}

// Close marks the Store closed. The underlying *opensearch.Client is
// caller-owned and is intentionally NOT touched — callers who never share
// the client can rely on Store.Close for idempotence.
func (s *Store) Close() error {
	s.closed.Store(true)
	return nil
}

// --- internals --------------------------------------------------------

// searchSource is the subset of the indexed document we care about at
// search time.
type searchSource struct {
	Metadata map[string]any `json:"metadata,omitempty"`
	Content  string         `json:"content,omitempty"`
}

// bulk executes a single _bulk request whose body is already fully built.
// op is a short opcode name used in error wrapping ("upsert" / "delete").
func (s *Store) bulk(ctx context.Context, body []byte, op string) error {
	req := opensearchapi.BulkReq{
		Body: bytes.NewReader(body),
	}
	var resp opensearchapi.BulkResp
	if _, err := s.do(ctx, req, &resp); err != nil {
		return fmt.Errorf("vectorstore/opensearch: bulk %s %q: %w", op, s.index, err)
	}
	if resp.Errors {
		return fmt.Errorf("vectorstore/opensearch: bulk %s %q: server reported item-level errors", op, s.index)
	}
	return nil
}

// do calls opensearch.Client.Do and converts non-2xx responses to Go
// errors. It centralises the response parsing so per-call sites only
// worry about their happy-path types.
func (s *Store) do(ctx context.Context, req opensearch.Request, out any) (*opensearch.Response, error) {
	resp, err := s.client.Do(ctx, req, out)
	if err != nil {
		return resp, err
	}
	if resp != nil && resp.IsError() {
		// Drain the body for the error message but ignore any decoding
		// error — we've already got a status code to report.
		var body string
		if resp.Body != nil {
			if b, rerr := io.ReadAll(resp.Body); rerr == nil {
				body = strings.TrimSpace(string(b))
			}
		}
		return resp, fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	return resp, nil
}

// buildIndexBulkBody constructs a newline-delimited _bulk body for a slice
// of docs. Each doc becomes two lines: an {"index": {...}} action and the
// document source.
func buildIndexBulkBody(index string, docs []vectorstore.Doc) ([]byte, error) {
	var buf bytes.Buffer
	for _, d := range docs {
		action := map[string]any{
			"index": map[string]any{
				"_index": index,
				"_id":    d.ID,
			},
		}
		ab, err := json.Marshal(action)
		if err != nil {
			return nil, fmt.Errorf("vectorstore/opensearch: marshal bulk action for %q: %w", d.ID, err)
		}
		src := map[string]any{
			"embedding": []float32(d.Vector),
		}
		if d.Content != "" {
			src["content"] = d.Content
		}
		if len(d.Metadata) > 0 {
			src["metadata"] = d.Metadata
		}
		sb, err := json.Marshal(src)
		if err != nil {
			return nil, fmt.Errorf("vectorstore/opensearch: marshal bulk source for %q: %w", d.ID, err)
		}
		buf.Write(ab)
		buf.WriteByte('\n')
		buf.Write(sb)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// buildDeleteBulkBody constructs a newline-delimited _bulk body of delete
// actions.
func buildDeleteBulkBody(index string, ids []string) []byte {
	var buf bytes.Buffer
	for _, id := range ids {
		// Delete action is a single-line entry with no source body.
		line := fmt.Sprintf(`{"delete":{"_index":%q,"_id":%q}}`+"\n", index, id)
		buf.WriteString(line)
	}
	return buf.Bytes()
}

// sortedKeys returns the keys of a filter map in ascending order. Used to
// keep generated request bodies deterministic.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple insertion sort — filters are tiny.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// errClosed is returned when any method is called after Close.
var errClosed = errors.New("vectorstore/opensearch: store is closed")
