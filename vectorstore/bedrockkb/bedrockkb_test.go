package bedrockkb

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	kbtypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"

	"oss.nandlabs.io/golly/vectorstore"
)

// --- fake ------------------------------------------------------------

// fakeRetrieveClient records the last Retrieve input and returns a
// caller-provided output/err.
type fakeRetrieveClient struct {
	last *bedrockagentruntime.RetrieveInput
	out  *bedrockagentruntime.RetrieveOutput
	err  error
	n    int
}

func (f *fakeRetrieveClient) Retrieve(_ context.Context, in *bedrockagentruntime.RetrieveInput, _ ...func(*bedrockagentruntime.Options)) (*bedrockagentruntime.RetrieveOutput, error) {
	f.n++
	f.last = in
	if f.err != nil {
		return nil, f.err
	}
	if f.out == nil {
		return &bedrockagentruntime.RetrieveOutput{}, nil
	}
	return f.out, nil
}

func newTestStore(t *testing.T) (*Store, *fakeRetrieveClient) {
	t.Helper()
	f := &fakeRetrieveClient{}
	s := newWithClient(f, "KB12345678")
	return s, f
}

// --- tests -----------------------------------------------------------

func TestUpsert_ReturnsNotSupported(t *testing.T) {
	s, f := newTestStore(t)
	err := s.Upsert(context.Background(), vectorstore.Doc{ID: "x", Vector: vectorstore.Vector{1}})
	if !errors.Is(err, vectorstore.ErrNotSupported) {
		t.Fatalf("want ErrNotSupported, got %v", err)
	}
	if f.n != 0 {
		t.Errorf("Retrieve should not have been called, got %d", f.n)
	}
}

func TestDelete_ReturnsNotSupported(t *testing.T) {
	s, f := newTestStore(t)
	err := s.Delete(context.Background(), "a", "b")
	if !errors.Is(err, vectorstore.ErrNotSupported) {
		t.Fatalf("want ErrNotSupported, got %v", err)
	}
	if f.n != 0 {
		t.Errorf("Retrieve should not have been called, got %d", f.n)
	}
}

func TestSearch_TextQuery_Success(t *testing.T) {
	s, f := newTestStore(t)
	score1, score2 := 0.91, 0.42
	f.out = &bedrockagentruntime.RetrieveOutput{
		RetrievalResults: []kbtypes.KnowledgeBaseRetrievalResult{
			{
				Content: &kbtypes.RetrievalResultContent{Text: aws.String("hello")},
				Location: &kbtypes.RetrievalResultLocation{
					Type:       kbtypes.RetrievalResultLocationTypeS3,
					S3Location: &kbtypes.RetrievalResultS3Location{Uri: aws.String("s3://bucket/a.txt")},
				},
				Score:      &score1,
				DocumentId: aws.String("doc-a"),
			},
			{
				Content:    &kbtypes.RetrievalResultContent{Text: aws.String("world")},
				Score:      &score2,
				DocumentId: aws.String("doc-b"),
			},
		},
	}
	hits, err := s.Search(context.Background(), vectorstore.Query{
		TopK:   4,
		Filter: map[string]any{QueryTextKey: "what is fnord?"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %d", len(hits))
	}
	// Hit 0 gets the S3 URI because Location.S3Location is set.
	if hits[0].ID != "s3://bucket/a.txt" || hits[0].Content != "hello" || hits[0].Score != float32(0.91) {
		t.Errorf("hit0 = %+v", hits[0])
	}
	// Hit 1 has no location, so we fall back to DocumentId.
	if hits[1].ID != "doc-b" || hits[1].Score != float32(0.42) {
		t.Errorf("hit1 = %+v", hits[1])
	}
	// Verify the outbound call: KB id, text, TopK.
	if f.last == nil {
		t.Fatal("Retrieve not called")
	}
	if got := aws.ToString(f.last.KnowledgeBaseId); got != "KB12345678" {
		t.Errorf("KnowledgeBaseId = %q", got)
	}
	if got := aws.ToString(f.last.RetrievalQuery.Text); got != "what is fnord?" {
		t.Errorf("text = %q", got)
	}
	n := f.last.RetrievalConfiguration.VectorSearchConfiguration.NumberOfResults
	if n == nil || *n != 4 {
		t.Errorf("NumberOfResults = %v", n)
	}
}

func TestSearch_EmptyText_Errors(t *testing.T) {
	s, f := newTestStore(t)
	// Only a raw vector, no text: this backend must refuse.
	_, err := s.Search(context.Background(), vectorstore.Query{
		Vector: vectorstore.Vector{0.1, 0.2, 0.3},
		TopK:   5,
	})
	if err == nil {
		t.Fatal("want error for missing text query, got nil")
	}
	if f.n != 0 {
		t.Errorf("Retrieve should not have been called, got %d", f.n)
	}
}

func TestSearch_MinScoreFilters(t *testing.T) {
	s, f := newTestStore(t)
	high, low := 0.9, 0.1
	f.out = &bedrockagentruntime.RetrieveOutput{
		RetrievalResults: []kbtypes.KnowledgeBaseRetrievalResult{
			{Content: &kbtypes.RetrievalResultContent{Text: aws.String("keep")}, Score: &high, DocumentId: aws.String("k")},
			{Content: &kbtypes.RetrievalResultContent{Text: aws.String("drop")}, Score: &low, DocumentId: aws.String("d")},
		},
	}
	hits, err := s.Search(context.Background(), vectorstore.Query{
		TopK:     10,
		MinScore: 0.5,
		Filter:   map[string]any{QueryTextKey: "q"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "k" {
		t.Fatalf("MinScore did not drop the low-score hit: %+v", hits)
	}
}

func TestSearch_DefaultTopK(t *testing.T) {
	s, f := newTestStore(t)
	if _, err := s.Search(context.Background(), vectorstore.Query{
		Filter: map[string]any{QueryTextKey: "q"},
	}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	n := f.last.RetrievalConfiguration.VectorSearchConfiguration.NumberOfResults
	if n == nil || *n != 10 {
		t.Errorf("default TopK = %v, want 10", n)
	}
}

func TestClose_NoOp(t *testing.T) {
	s, f := newTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	// After Close every operation should fail fast without touching the wire.
	if err := s.Upsert(context.Background(), vectorstore.Doc{ID: "x", Vector: vectorstore.Vector{1}}); err == nil {
		t.Error("Upsert after Close should fail")
	}
	if _, err := s.Search(context.Background(), vectorstore.Query{Filter: map[string]any{QueryTextKey: "q"}}); err == nil {
		t.Error("Search after Close should fail")
	}
	if err := s.Delete(context.Background(), "x"); err == nil {
		t.Error("Delete after Close should fail")
	}
	if f.n != 0 {
		t.Errorf("no wire calls expected, got %d", f.n)
	}
}

func TestNew_ValidatesOptions(t *testing.T) {
	if _, err := New(Options{KnowledgeBaseID: "k"}); err == nil {
		t.Error("expected error when Client missing")
	}
	if _, err := New(Options{Client: &bedrockagentruntime.Client{}}); err == nil {
		t.Error("expected error when KnowledgeBaseID missing")
	}
}
