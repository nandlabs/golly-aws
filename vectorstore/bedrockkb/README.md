# vectorstore/bedrockkb

Read-only [`oss.nandlabs.io/golly/vectorstore`](https://pkg.go.dev/oss.nandlabs.io/golly/vectorstore)
backend for Amazon Bedrock Knowledge Bases, using the
`bedrock-agent-runtime` [Retrieve API].

Bedrock owns the ingestion pipeline (it embeds and indexes documents in
its managed vector store from an attached data source), so this backend
supports **only** retrieval. `Upsert` and `Delete` return
`vectorstore.ErrNotSupported`.

[Retrieve API]: https://docs.aws.amazon.com/bedrock/latest/APIReference/API_agent-runtime_Retrieve.html

## Install

```bash
go get oss.nandlabs.io/golly-aws/vectorstore/bedrockkb
```

## Quick start

```go
import (
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
    "oss.nandlabs.io/golly-aws/vectorstore/bedrockkb"
    "oss.nandlabs.io/golly/vectorstore"
)

cfg, _ := config.LoadDefaultConfig(ctx)
c := bedrockagentruntime.NewFromConfig(cfg)

store, err := bedrockkb.New(bedrockkb.Options{
    Client:          c,
    KnowledgeBaseID: "ABC1234567",              // 10-char KB id
})
if err != nil { return err }
defer store.Close()

hits, err := store.Search(ctx, vectorstore.Query{
    TopK:   5,
    Filter: map[string]any{bedrockkb.QueryTextKey: "what is fnord?"},
})
```

## Populating the Knowledge Base

This package does not ingest documents. Attach an S3 (or Confluence,
Salesforce, SharePoint, ...) data source to your Knowledge Base and drive
ingestion with `StartIngestionJob` on the `bedrock-agent` control-plane
API — outside golly. See the
[Bedrock KB data-source workflow](https://docs.aws.amazon.com/bedrock/latest/userguide/knowledge-base-ds.html).

## Notes

- **Text queries, not vectors.** Bedrock runs the embedding model
  server-side. Pass your query text via `Query.Filter["_text"]` (the
  reserved key `bedrockkb.QueryTextKey`); a `Query` carrying only a raw
  `Vector` is rejected with an explanatory error.
- **Hit.ID** is the best-effort data-source URI (S3 `s3://…`, Kendra
  URI, web URL, ...) so callers can round-trip to the origin document.
  When no location URI is available the KB `DocumentId` is used.
- **Score.** `Hit.Score` is the Retrieve API's own relevance score
  cast from `float64` to `float32`.
- **Metadata.** Bedrock's `document.Interface` values are unmarshalled
  into plain Go types (`map[string]any`). Values that fail to unmarshal
  are silently dropped.
- **Write / delete.** `Upsert` and `Delete` return `vectorstore.ErrNotSupported`
  — see the note above about the ingestion pipeline.
