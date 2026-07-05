# vectorstore/opensearch

OpenSearch kNN-plugin backend for the
[`oss.nandlabs.io/golly/vectorstore`](https://pkg.go.dev/oss.nandlabs.io/golly/vectorstore)
`Store` interface. Talks to any OpenSearch cluster whose target index has
the kNN plugin enabled — Amazon OpenSearch Service, OpenSearch Serverless,
or self-hosted.

## Install

```bash
go get oss.nandlabs.io/golly-aws/vectorstore/opensearch
```

## Quick start

```go
import (
    osgo "github.com/opensearch-project/opensearch-go/v3"
    "oss.nandlabs.io/golly-aws/vectorstore/opensearch"
    "oss.nandlabs.io/golly/vectorstore"
)

c, _ := osgo.NewClient(osgo.Config{Addresses: []string{"https://search.example.com"}})
store, err := opensearch.New(opensearch.Options{
    Client:    c,
    Index:     "docs",
    Dimension: 768,                            // used by CreateIndex helper
    Space:     opensearch.SpaceCosine,         // or SpaceL2 / SpaceIP
})
if err != nil { return err }
defer store.Close()                             // does NOT close the OS client

// Optional: bootstrap the index with a kNN mapping.
_ = store.CreateIndex(ctx)

_ = store.Upsert(ctx, vectorstore.Doc{ID: "d1", Vector: emb, Metadata: map[string]any{"src": "wiki"}})
hits, _ := store.Search(ctx, vectorstore.Query{Vector: qEmb, TopK: 10})
```

## Index mapping (bootstrap)

`CreateIndex` writes this mapping — feed it to your own migration tool if
you'd rather manage the schema out of band:

```json
{
  "settings": {"index.knn": true},
  "mappings": {
    "properties": {
      "embedding": {
        "type": "knn_vector",
        "dimension": <Dimension>,
        "method": {"name":"hnsw","space_type":"<Space>","engine":"<KnnEngine>"}
      },
      "content":  {"type": "text"},
      "metadata": {"type": "object"}
    }
  }
}
```

## Notes

- **Caller-owned index.** `New` does NOT create the target index. Use
  `CreateIndex(ctx)` to bootstrap, or manage the mapping externally.
- **Bulk batching.** `Upsert` and `Delete` split into `_bulk` calls of up
  to 500 items each (configurable via `Options.BulkBatchSize`).
- **Filter dialect.** `Query.Filter` is translated into a
  `bool.must` list of `term` clauses on `metadata.<key>` — one equality
  predicate per filter entry. Nested boolean logic is not supported.
- **Score.** `Hit.Score` is the OpenSearch `_score` value untouched.
  Higher is better for `cosinesimil`; interpret accordingly for other
  spaces.
- **Close.** `Store.Close()` marks the Store closed but does not touch
  the `*opensearch.Client` — that handle is caller-owned.
