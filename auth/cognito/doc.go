// Package cognito provides an AWS Cognito User Pool adapter for the
// golly auth primitives.
//
// It ships two building blocks that plug into oss.nandlabs.io/golly/auth:
//
//   - Verifier: a JWKS-backed JWT verifier for tokens minted by an
//     Amazon Cognito User Pool. It fetches the public keys from
//     https://cognito-idp.<region>.amazonaws.com/<userPoolID>/.well-known/jwks.json,
//     caches them per-kid with a configurable TTL, and validates the
//     RS256 signature plus the issuer, token_use, audience/client_id,
//     expiry and subject claims. Both id-tokens (token_use=id, aud) and
//     access-tokens (token_use=access, client_id) are accepted by
//     default; use WithTokenUse to narrow.
//
//   - DynamoDBSessionStore: an auth.SessionStore implementation backed
//     by Amazon DynamoDB. Sessions are serialized as JSON documents
//     keyed by session id and can be queried by their subject via the
//     ListByUser helper (requires a GSI named "userID-index"). The
//     mirrored expires_at attribute is Unix-seconds encoded and is
//     suitable as the DynamoDB TTL attribute so expired sessions are
//     pruned server-side.
//
// Neither type registers itself with any manager: callers wire them
// explicitly into their golly auth verification or session middleware.
package cognito
