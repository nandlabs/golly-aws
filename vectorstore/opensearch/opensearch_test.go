package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	osgo "github.com/opensearch-project/opensearch-go/v3"

	"oss.nandlabs.io/golly/vectorstore"
)

// --- test harness ------------------------------------------------------

// capturedReq is one HTTP call the fake OpenSearch server observed.
type capturedReq struct {
	Method string
	Path   string
	Body   []byte
}

// fakeServer is a tiny router around httptest.Server that records every
// incoming request and lets a test rewrite its response per-path.
type fakeServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []capturedReq
	// responder returns (status, body) for a given method + path.
	responder func(method, path string, body []byte) (int, string)
}

func newFakeServer(responder func(method, path string, body []byte) (int, string)) *fakeServer {
	fs := &fakeServer{responder: responder}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()

		// Some code paths in the client precheck the cluster with a HEAD or
		// GET / — respond with a minimal info doc so those don't blow up if
		// the transport happens to issue them.
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"version":{"number":"2.11.0","distribution":"opensearch"},"tagline":"The OpenSearch Project: https://opensearch.org/"}`))
			return
		}

		fs.mu.Lock()
		fs.requests = append(fs.requests, capturedReq{
			Method: r.Method,
			Path:   r.URL.Path,
			Body:   append([]byte(nil), body...),
		})
		responder := fs.responder
		fs.mu.Unlock()

		status, respBody := responder(r.Method, r.URL.Path, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
	return fs
}

func (fs *fakeServer) captured() []capturedReq {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	out := make([]capturedReq, len(fs.requests))
	copy(out, fs.requests)
	return out
}

// newTestStore returns a Store wired to a fakeServer plus the server itself
// so tests can inspect captured requests.
func newTestStore(t *testing.T, responder func(method, path string, body []byte) (int, string)) (*Store, *fakeServer) {
	t.Helper()
	if responder == nil {
		responder = func(_, _ string, _ []byte) (int, string) { return 200, `{}` }
	}
	fs := newFakeServer(responder)
	t.Cleanup(fs.Close)

	c, err := osgo.NewClient(osgo.Config{
		Addresses:    []string{fs.URL},
		DisableRetry: true,
	})
	if err != nil {
		t.Fatalf("new opensearch client: %v", err)
	}
	s, err := New(Options{
		Client:    c,
		Index:     "docs",
		Dimension: 3,
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return s, fs
}

// bulkOKResponse is the minimal happy-path body for a _bulk call.
const bulkOKResponse = `{"took":1,"errors":false,"items":[]}`

// --- tests -------------------------------------------------------------

func TestUpsert_RoundTrip(t *testing.T) {
	s, fs := newTestStore(t, func(method, path string, _ []byte) (int, string) {
		if path == "/_bulk" && method == http.MethodPost {
			return 200, bulkOKResponse
		}
		return 200, `{}`
	})
	docs := []vectorstore.Doc{
		{ID: "a", Vector: vectorstore.Vector{1, 2, 3}, Metadata: map[string]any{"source": "wiki"}, Content: "hello"},
		{ID: "b", Vector: vectorstore.Vector{4, 5, 6}, Metadata: map[string]any{"rank": 42}},
	}
	if err := s.Upsert(context.Background(), docs...); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	reqs := fs.captured()
	if len(reqs) != 1 {
		t.Fatalf("want 1 bulk call, got %d", len(reqs))
	}
	if reqs[0].Path != "/_bulk" || reqs[0].Method != http.MethodPost {
		t.Errorf("unexpected request: %s %s", reqs[0].Method, reqs[0].Path)
	}
	body := string(reqs[0].Body)
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("want 4 bulk lines (2 docs * (action+source)), got %d: %q", len(lines), body)
	}
	// Line 0: index action for "a"
	var action struct {
		Index struct {
			IndexName string `json:"_index"`
			ID        string `json:"_id"`
		} `json:"index"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &action); err != nil {
		t.Fatalf("parse action line: %v", err)
	}
	if action.Index.IndexName != "docs" || action.Index.ID != "a" {
		t.Errorf("action[0] = %+v", action)
	}
	// Line 1: source for "a" — must have embedding + metadata + content.
	var src struct {
		Embedding []float32      `json:"embedding"`
		Metadata  map[string]any `json:"metadata"`
		Content   string         `json:"content"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &src); err != nil {
		t.Fatalf("parse source line: %v", err)
	}
	if len(src.Embedding) != 3 || src.Embedding[0] != 1 {
		t.Errorf("embedding = %v", src.Embedding)
	}
	if src.Metadata["source"] != "wiki" {
		t.Errorf("metadata = %v", src.Metadata)
	}
	if src.Content != "hello" {
		t.Errorf("content = %q", src.Content)
	}
}

func TestUpsert_Batching_500(t *testing.T) {
	s, fs := newTestStore(t, func(method, path string, _ []byte) (int, string) {
		if path == "/_bulk" {
			return 200, bulkOKResponse
		}
		return 200, `{}`
	})
	docs := make([]vectorstore.Doc, 1200)
	for i := range docs {
		docs[i] = vectorstore.Doc{
			ID:     fmt.Sprintf("doc-%04d", i),
			Vector: vectorstore.Vector{float32(i), float32(i + 1), float32(i + 2)},
		}
	}
	if err := s.Upsert(context.Background(), docs...); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	reqs := fs.captured()
	if len(reqs) != 3 {
		t.Fatalf("want 3 bulk calls for 1200 docs (batch=500), got %d", len(reqs))
	}
	// Batches should contain 500, 500, 200 sources — each source is one line
	// after each action line, so 1000 / 1000 / 400 non-empty lines total.
	for i, want := range []int{1000, 1000, 400} {
		body := strings.TrimRight(string(reqs[i].Body), "\n")
		got := len(strings.Split(body, "\n"))
		if got != want {
			t.Errorf("batch %d: want %d bulk lines, got %d", i, want, got)
		}
	}
}

func TestUpsert_RejectsInvalidDoc(t *testing.T) {
	s, _ := newTestStore(t, nil)
	err := s.Upsert(context.Background(), vectorstore.Doc{ID: "", Vector: vectorstore.Vector{1, 2, 3}})
	if err == nil {
		t.Fatal("want error for empty id, got nil")
	}
}

func TestSearch_ReturnsHits(t *testing.T) {
	body := `{"took":1,"timed_out":false,"_shards":{"total":1,"successful":1,"failed":0},` +
		`"hits":{"total":{"value":2,"relation":"eq"},"max_score":0.99,"hits":[` +
		`{"_index":"docs","_id":"a","_score":0.99,"_source":{"metadata":{"source":"wiki"},"content":"hello"}},` +
		`{"_index":"docs","_id":"b","_score":0.87,"_source":{"metadata":{"source":"blog"}}}` +
		`]}}`
	s, fs := newTestStore(t, func(method, path string, _ []byte) (int, string) {
		if path == "/docs/_search" {
			return 200, body
		}
		return 200, `{}`
	})
	hits, err := s.Search(context.Background(), vectorstore.Query{
		Vector: vectorstore.Vector{0.1, 0.2, 0.3},
		TopK:   5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %d", len(hits))
	}
	if hits[0].ID != "a" || hits[0].Score != 0.99 || hits[0].Content != "hello" {
		t.Errorf("hit0 = %+v", hits[0])
	}
	if hits[1].ID != "b" || hits[1].Metadata["source"] != "blog" {
		t.Errorf("hit1 = %+v", hits[1])
	}
	reqs := fs.captured()
	if len(reqs) != 1 || reqs[0].Path != "/docs/_search" {
		t.Fatalf("unexpected requests: %+v", reqs)
	}
	// Verify the search body carries the knn subquery with the right vector and k.
	var got map[string]any
	if err := json.Unmarshal(reqs[0].Body, &got); err != nil {
		t.Fatalf("parse search body: %v", err)
	}
	if got["size"] != float64(5) {
		t.Errorf("size = %v", got["size"])
	}
	q := got["query"].(map[string]any)["knn"].(map[string]any)["embedding"].(map[string]any)
	if q["k"] != float64(5) {
		t.Errorf("k = %v", q["k"])
	}
	if _, ok := q["vector"]; !ok {
		t.Errorf("missing vector: %+v", q)
	}
}

func TestSearch_MetadataFilter(t *testing.T) {
	s, fs := newTestStore(t, func(method, path string, _ []byte) (int, string) {
		if path == "/docs/_search" {
			return 200, `{"took":0,"timed_out":false,"_shards":{},"hits":{"total":{"value":0},"hits":[]}}`
		}
		return 200, `{}`
	})
	_, err := s.Search(context.Background(), vectorstore.Query{
		Vector: vectorstore.Vector{0.1, 0.2, 0.3},
		TopK:   3,
		Filter: map[string]any{"source": "wiki", "rank": 42},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	reqs := fs.captured()
	if len(reqs) != 1 {
		t.Fatalf("want 1 request, got %d", len(reqs))
	}
	var body map[string]any
	if err := json.Unmarshal(reqs[0].Body, &body); err != nil {
		t.Fatalf("parse: %v", err)
	}
	embed := body["query"].(map[string]any)["knn"].(map[string]any)["embedding"].(map[string]any)
	filter, ok := embed["filter"].(map[string]any)
	if !ok {
		t.Fatalf("expected knn.filter, body = %s", reqs[0].Body)
	}
	must := filter["bool"].(map[string]any)["must"].([]any)
	if len(must) != 2 {
		t.Fatalf("want 2 must clauses, got %d: %+v", len(must), must)
	}
	// Because sortedKeys is deterministic ("rank" < "source"), we know the order.
	terms := make(map[string]any)
	for _, c := range must {
		termObj := c.(map[string]any)["term"].(map[string]any)
		for k, v := range termObj {
			terms[k] = v
		}
	}
	if terms["metadata.source"] != "wiki" || terms["metadata.rank"].(float64) != 42 {
		t.Errorf("terms = %+v", terms)
	}
}

func TestSearch_EmptyVector(t *testing.T) {
	s, _ := newTestStore(t, nil)
	if _, err := s.Search(context.Background(), vectorstore.Query{Vector: nil, TopK: 3}); err == nil {
		t.Fatal("want error for empty vector")
	}
}

func TestDelete_RemovesById(t *testing.T) {
	s, fs := newTestStore(t, func(method, path string, _ []byte) (int, string) {
		if path == "/_bulk" {
			return 200, bulkOKResponse
		}
		return 200, `{}`
	})
	if err := s.Delete(context.Background(), "a", "b", "c"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	reqs := fs.captured()
	if len(reqs) != 1 {
		t.Fatalf("want 1 bulk call, got %d", len(reqs))
	}
	body := strings.TrimRight(string(reqs[0].Body), "\n")
	lines := strings.Split(body, "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 delete lines, got %d: %q", len(lines), body)
	}
	for i, id := range []string{"a", "b", "c"} {
		var got struct {
			Delete struct {
				Index string `json:"_index"`
				ID    string `json:"_id"`
			} `json:"delete"`
		}
		if err := json.Unmarshal([]byte(lines[i]), &got); err != nil {
			t.Fatalf("parse line %d: %v", i, err)
		}
		if got.Delete.Index != "docs" || got.Delete.ID != id {
			t.Errorf("line %d = %+v (want id=%q)", i, got, id)
		}
	}
}

func TestDelete_EmptyIsNoop(t *testing.T) {
	s, fs := newTestStore(t, nil)
	if err := s.Delete(context.Background()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := len(fs.captured()); got != 0 {
		t.Errorf("expected no RPC for empty delete, got %d", got)
	}
}

func TestClose_Idempotent(t *testing.T) {
	s, _ := newTestStore(t, nil)
	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	// After Close every operation should fail fast without touching the wire.
	if err := s.Upsert(context.Background(), vectorstore.Doc{ID: "x", Vector: vectorstore.Vector{1, 2, 3}}); err == nil {
		t.Error("Upsert after Close should fail")
	}
	if _, err := s.Search(context.Background(), vectorstore.Query{Vector: vectorstore.Vector{1, 2, 3}}); err == nil {
		t.Error("Search after Close should fail")
	}
	if err := s.Delete(context.Background(), "x"); err == nil {
		t.Error("Delete after Close should fail")
	}
}

func TestNew_ValidatesOptions(t *testing.T) {
	c, err := osgo.NewClient(osgo.Config{Addresses: []string{"http://127.0.0.1:1"}})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		o    Options
	}{
		{"no client", Options{Index: "docs"}},
		{"no index", Options{Client: c}},
		{"bad space", Options{Client: c, Index: "docs", Space: "not-a-metric"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.o); err == nil {
				t.Error("expected validation error")
			}
		})
	}
}

func TestCreateIndex_WritesMapping(t *testing.T) {
	var got []byte
	s, _ := newTestStore(t, func(method, path string, body []byte) (int, string) {
		if method == http.MethodPut && path == "/docs" {
			got = append([]byte(nil), body...)
			return 200, `{"acknowledged":true,"shards_acknowledged":true,"index":"docs"}`
		}
		return 200, `{}`
	})
	if err := s.CreateIndex(context.Background()); err != nil {
		t.Fatalf("CreateIndex: %v", err)
	}
	// Sanity-check: mapping mentions knn_vector, dimension 3, our default space,
	// and index.knn is turned on.
	if !bytes.Contains(got, []byte(`"knn_vector"`)) {
		t.Errorf("mapping missing knn_vector: %s", got)
	}
	if !bytes.Contains(got, []byte(`"dimension":3`)) {
		t.Errorf("mapping missing dimension: %s", got)
	}
	if !bytes.Contains(got, []byte(`"index.knn":true`)) {
		t.Errorf("mapping missing index.knn: %s", got)
	}
	if !bytes.Contains(got, []byte(`"space_type":"cosinesimil"`)) {
		t.Errorf("mapping missing space_type: %s", got)
	}
}
