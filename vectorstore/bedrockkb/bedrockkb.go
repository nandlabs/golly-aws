// Package bedrockkb is a read-only vectorstore backend that queries an
// Amazon Bedrock Knowledge Base via the bedrock-agent-runtime Retrieve API.
//
// Knowledge Bases are populated by Bedrock's own ingestion pipeline from an
// attached data source (S3, Confluence, Salesforce, SharePoint, ...) — this
// package therefore surfaces Upsert / Delete as ErrNotSupported. Callers
// hydrate the KB out of band and use this backend only for retrieval.
//
// Query semantics differ from the other vectorstore backends in one
// important way: Bedrock KB Retrieve takes a **text query**, not a raw
// vector. Bedrock runs the embedding model server-side. Callers therefore
// supply the natural-language question via the reserved filter key
// [QueryTextKey] ("_text") on vectorstore.Query.Filter — this backend
// treats it as the retrieval query and forwards the remaining filter keys
// unchanged. A Query that provides only Query.Vector with no "_text" is
// rejected with an explanatory error.
//
// The Store is safe for concurrent use.
package bedrockkb

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/document"
	kbtypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"

	"oss.nandlabs.io/golly/vectorstore"
)

// retrieveAPI is the minimal surface of *bedrockagentruntime.Client that the
// Store exercises. Extracted as an interface so tests can inject fakes.
type retrieveAPI interface {
	Retrieve(ctx context.Context, params *bedrockagentruntime.RetrieveInput, optFns ...func(*bedrockagentruntime.Options)) (*bedrockagentruntime.RetrieveOutput, error)
}

// Options configures a Store.
type Options struct {
	// Client is a caller-owned bedrockagentruntime.Client. Required.
	// Store.Close does not touch it — the caller owns the client.
	Client *bedrockagentruntime.Client
	// KnowledgeBaseID is the 10-character Bedrock Knowledge Base identifier
	// (e.g. "ABC1234567"). Required.
	KnowledgeBaseID string
}

// Store implements vectorstore.Store for read-only retrieval from a Bedrock
// Knowledge Base. Upsert and Delete return vectorstore.ErrNotSupported
// because KB ingestion is driven by the Bedrock data-source pipeline.
type Store struct {
	client retrieveAPI
	kbID   string
	closed atomic.Bool
}

// New constructs a Store from Options. It performs eager validation of the
// required fields but does not make any network calls.
func New(opts Options) (*Store, error) {
	if opts.Client == nil {
		return nil, errors.New("vectorstore/bedrockkb: Options.Client is required")
	}
	if opts.KnowledgeBaseID == "" {
		return nil, errors.New("vectorstore/bedrockkb: Options.KnowledgeBaseID is required")
	}
	return newWithClient(opts.Client, opts.KnowledgeBaseID), nil
}

// newWithClient is the internal constructor used by New and by tests.
func newWithClient(c retrieveAPI, kbID string) *Store {
	return &Store{client: c, kbID: kbID}
}

// Upsert is not supported by this backend. Bedrock Knowledge Bases are
// populated by the KB's data-source ingestion pipeline (S3 sync, Confluence
// crawl, ...) and expose no direct write API.
func (s *Store) Upsert(_ context.Context, _ ...vectorstore.Doc) error {
	if s.closed.Load() {
		return errClosed
	}
	return fmt.Errorf("%w: ingest via the Bedrock Knowledge Base data-source pipeline — direct Upsert is not supported by this backend",
		vectorstore.ErrNotSupported)
}

// QueryTextKey is the reserved vectorstore.Query.Filter key this backend
// reads to obtain the natural-language retrieval query. See package doc.
const QueryTextKey = "_text"

// Search issues a Retrieve call against the configured Knowledge Base and
// maps RetrievalResults onto []vectorstore.Hit.
//
// The retrieval text is read from q.Filter[QueryTextKey] (a string). A
// query that carries only q.Vector is rejected — Bedrock runs the
// embedding model itself and does not accept a raw vector on Retrieve.
//
// The Hit fields map as follows:
//   - ID       — best-effort location URI (S3 URI, Kendra doc URI, web
//     URL, ...) so callers can round-trip to the original document. When
//     no location URI is available the DocumentId is used.
//   - Score    — the Retrieve API's own relevance score.
//   - Content  — RetrievalResults[i].Content.Text.
//   - Metadata — the KB metadata attribute map, converted from Bedrock's
//     document.Interface values to Go plain types.
func (s *Store) Search(ctx context.Context, q vectorstore.Query) ([]vectorstore.Hit, error) {
	if s.closed.Load() {
		return nil, errClosed
	}
	text, _ := q.Filter[QueryTextKey].(string)
	if text == "" {
		return nil, fmt.Errorf("vectorstore/bedrockkb: Bedrock KB Retrieve requires a text query, not a raw vector — set Query.Filter[%q] to the natural-language query (Bedrock runs the embedding model server-side)", QueryTextKey)
	}

	topK := q.TopK
	if topK <= 0 {
		topK = 10
	}
	//nolint:gosec // TopK is caller-controlled and bounded well below int32 max.
	numResults := int32(topK)

	in := &bedrockagentruntime.RetrieveInput{
		KnowledgeBaseId: aws.String(s.kbID),
		RetrievalQuery: &kbtypes.KnowledgeBaseQuery{
			Text: aws.String(text),
		},
		RetrievalConfiguration: &kbtypes.KnowledgeBaseRetrievalConfiguration{
			VectorSearchConfiguration: &kbtypes.KnowledgeBaseVectorSearchConfiguration{
				NumberOfResults: &numResults,
			},
		},
	}
	out, err := s.client.Retrieve(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("vectorstore/bedrockkb: Retrieve: %w", err)
	}
	hits := make([]vectorstore.Hit, 0, len(out.RetrievalResults))
	for _, r := range out.RetrievalResults {
		var score float32
		if r.Score != nil {
			score = float32(*r.Score)
		}
		if q.MinScore != 0 && score < q.MinScore {
			continue
		}
		hits = append(hits, vectorstore.Hit{
			ID:       hitIDFor(r),
			Score:    score,
			Content:  contentText(r.Content),
			Metadata: metadataAsAny(r.Metadata),
		})
	}
	return hits, nil
}

// Delete is not supported by this backend — see Upsert.
func (s *Store) Delete(_ context.Context, _ ...string) error {
	if s.closed.Load() {
		return errClosed
	}
	return fmt.Errorf("%w: delete documents from the Bedrock Knowledge Base data source and re-sync — direct Delete is not supported by this backend",
		vectorstore.ErrNotSupported)
}

// Close marks the Store closed. The underlying bedrockagentruntime client
// is caller-owned and is intentionally NOT touched.
func (s *Store) Close() error {
	s.closed.Store(true)
	return nil
}

// --- helpers ----------------------------------------------------------

// contentText extracts a plain string from a Bedrock retrieval-result content
// object. Text is the common case; other content types (byte, video, audio,
// row/column) fall through to an empty string — callers who need richer
// content should read the Bedrock SDK types directly.
func contentText(c *kbtypes.RetrievalResultContent) string {
	if c == nil || c.Text == nil {
		return ""
	}
	return *c.Text
}

// hitIDFor picks the best identifier for a KB retrieval result. We prefer a
// concrete data-source URI (round-trippable to the origin), then fall back
// to the DocumentId supplied by the KB.
func hitIDFor(r kbtypes.KnowledgeBaseRetrievalResult) string {
	if uri := locationURI(r.Location); uri != "" {
		return uri
	}
	if r.DocumentId != nil {
		return *r.DocumentId
	}
	return ""
}

// locationURI walks the discriminated union of KB location types and returns
// the first non-empty URI it finds. Each concrete location type carries its
// own URI/URL/Uri field; we surface whichever one is set.
func locationURI(l *kbtypes.RetrievalResultLocation) string {
	if l == nil {
		return ""
	}
	if l.S3Location != nil && l.S3Location.Uri != nil {
		return *l.S3Location.Uri
	}
	if l.WebLocation != nil && l.WebLocation.Url != nil {
		return *l.WebLocation.Url
	}
	if l.ConfluenceLocation != nil && l.ConfluenceLocation.Url != nil {
		return *l.ConfluenceLocation.Url
	}
	if l.SalesforceLocation != nil && l.SalesforceLocation.Url != nil {
		return *l.SalesforceLocation.Url
	}
	if l.SharePointLocation != nil && l.SharePointLocation.Url != nil {
		return *l.SharePointLocation.Url
	}
	if l.OneDriveLocation != nil && l.OneDriveLocation.Url != nil {
		return *l.OneDriveLocation.Url
	}
	if l.KendraDocumentLocation != nil && l.KendraDocumentLocation.Uri != nil {
		return *l.KendraDocumentLocation.Uri
	}
	if l.GoogleDriveLocation != nil && l.GoogleDriveLocation.Url != nil {
		return *l.GoogleDriveLocation.Url
	}
	if l.CustomDocumentLocation != nil && l.CustomDocumentLocation.Id != nil {
		return *l.CustomDocumentLocation.Id
	}
	return ""
}

// metadataAsAny converts the Bedrock document.Interface metadata map — an
// opaque JSON-shaped SDK type — into a plain map[string]any that vectorstore
// callers can use. When conversion fails we drop the offending key: metadata
// is best-effort context.
func metadataAsAny(md map[string]document.Interface) map[string]any {
	if len(md) == 0 {
		return nil
	}
	out := make(map[string]any, len(md))
	for k, v := range md {
		if v == nil {
			continue
		}
		var dst any
		if err := v.UnmarshalSmithyDocument(&dst); err != nil {
			continue
		}
		out[k] = dst
	}
	return out
}

// errClosed is returned when any method is called after Close.
var errClosed = errors.New("vectorstore/bedrockkb: store is closed")
