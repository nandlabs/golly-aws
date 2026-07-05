# auth/cognito

AWS Cognito User Pool adapter for [`oss.nandlabs.io/golly/auth`](https://pkg.go.dev/oss.nandlabs.io/golly/auth).

Ships two pluggable pieces:

- **`Verifier`** — a JWKS-backed JWT verifier for Cognito User Pool
  tokens. It fetches the public keys from
  `https://cognito-idp.<region>.amazonaws.com/<userPoolID>/.well-known/jwks.json`,
  caches them per-kid with a configurable TTL (default 1h) and
  refreshes on demand when a token references an unknown kid. It
  validates the RS256 signature, `iss` (`https://cognito-idp.<region>.amazonaws.com/<userPoolID>`),
  `token_use` (accepts both `id` and `access` by default; narrow with
  `WithTokenUse`), the app client id (via `WithAppClientID` — `aud`
  for id-tokens, `client_id` for access-tokens), `exp` / `iat` with
  a configurable leeway, and a non-empty `sub`. Cognito-specific
  claims (`cognito:groups`, `cognito:username`, `token_use`,
  `client_id`) are surfaced via `Claims.Extra`.
- **`DynamoDBSessionStore`** — an `auth.SessionStore` backed by
  Amazon DynamoDB. Sessions are `json.Marshal`ed into the `payload`
  attribute of an item keyed by `id`; `user_id` and `expires_at`
  mirror the session fields at the top level. A `ListByUser(ctx, uid)`
  helper queries a Global Secondary Index named `userID-index`.

## Quick start — verify a Cognito JWT in a `rest` handler

```go
import (
    "net/http"

    "oss.nandlabs.io/golly-aws/auth/cognito"
    "oss.nandlabs.io/golly/rest"
)

func main() {
    v, err := cognito.NewVerifier(
        "us-east-1",
        "us-east-1_ABCdef123",
        cognito.WithAppClientID("your-app-client-id"),
    )
    if err != nil { panic(err) }
    r := rest.NewRouter()
    r.Get("/me", func(w http.ResponseWriter, req *http.Request) {
        tok := req.Header.Get("Authorization")[len("Bearer "):]
        claims, err := v.VerifyToken(req.Context(), tok)
        if err != nil { http.Error(w, err.Error(), http.StatusUnauthorized); return }
        w.Write([]byte("hello " + claims.Subject))
    })
    http.ListenAndServe(":8080", r)
}
```

## Session store

```go
client := dynamodb.NewFromConfig(cfg)
store := cognito.NewDynamoDBSessionStore(client, "sessions")
sm := auth.NewSessionManager(store, auth.SessionConfig{Absolute: 24 * time.Hour})
```

### Table schema

Partition key `id` (string). The store also writes `user_id` (S),
`payload` (S — JSON) and `expires_at` (N — Unix seconds). To prune
expired sessions server-side, **enable DynamoDB TTL on the table with
`expires_at` as the TTL attribute** (AWS Console → Table → Additional
settings → Time to live → attribute name `expires_at`).

`ListByUser(ctx, uid)` requires a Global Secondary Index named
`userID-index` with partition key `user_id` (S). If the GSI is
absent the call returns an error wrapping `ErrNotSupported` with the
verbatim DynamoDB `ValidationException`.
