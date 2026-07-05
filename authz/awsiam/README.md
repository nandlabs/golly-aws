# awsiam — AWS IAM authorizer for `golly/authz`

`awsiam` adapts AWS IAM to the `oss.nandlabs.io/golly/authz.Policy`
interface, so IAM policies bound to AWS users or roles can be used as
the source of truth for `Can(principal, action, resource)` checks in a
Golly app.

## Why this package looks the way it does

`authz.Policy.Can(p Principal, action string, resource any) bool` is
deliberately context-free and returns a plain bool. That doesn't fit an
AWS `iam:SimulatePrincipalPolicy` call, which needs a `context.Context`
and can fail with a network error. This package offers two shapes:

- **Cached evaluator (recommended)** — a background goroutine refreshes
  an allowed-actions map on a configurable interval. `Can` reads from
  the map. Freshness is bounded by the refresh interval; latency is
  amortised across many `Can` calls.
- **Live check via `IAMResource`** — pass an `IAMResource{Ctx, Arn}` as
  the `resource any` argument. `Can` type-asserts, then makes a live
  `iam:SimulatePrincipalPolicy` round-trip for that single action. No
  cache is consulted or updated.

## Usage

```go
import (
    "context"
    "time"

    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/iam"
    "oss.nandlabs.io/golly/authz"
    "oss.nandlabs.io/golly-aws/authz/awsiam"
)

func main() {
    ctx := context.Background()

    // 1. AWS IAM client — reuse across evaluators.
    cfg, err := config.LoadDefaultConfig(ctx)
    if err != nil { /* handle */ }
    iamClient := iam.NewFromConfig(cfg)

    principalArn := "arn:aws:iam::123456789012:role/svc"
    bucketArn := "arn:aws:s3:::my-bucket"

    // 2. Cached IAM evaluator, refreshed every minute.
    eval := awsiam.NewCachedEvaluator(iamClient, principalArn, bucketArn,
        []string{"s3:GetObject", "s3:ListBucket"},
        time.Minute,
        awsiam.WithExpectedMember(principalArn),
    )
    defer eval.Close()

    // 3. Compose with any other Policy — here RBAC as the local fallback.
    rbac := authz.NewRBAC()
    rbac.AddRole("admin", "s3:GetObject")

    policy := authz.Any(eval, rbac) // IAM says yes OR RBAC says yes

    // 4. Cached lookup: no round-trip.
    p := &myPrincipal{id: principalArn}
    if policy.Can(p, "s3:GetObject", nil) {
        // proceed
    }

    // 5. Live per-call check against a specific object ARN — bypasses
    //    the cache and hits IAM synchronously.
    live := awsiam.IAMResource{Ctx: ctx, Arn: "arn:aws:s3:::my-bucket/reports/2026.pdf"}
    if eval.Can(p, "s3:GetObject", live) {
        // proceed
    }
}
```

`myPrincipal` needs `Roles()`, `Capabilities()`, and either an
`ID() string` accessor (picked up by the default extractor) or one
injected with `awsiam.WithPrincipalMember(func(p authz.Principal) string { ... })`.
`Arn()` and `Member()` accessors are also recognised by the default
extractor.

## Options

| Option                        | Effect                                                                       |
|-------------------------------|------------------------------------------------------------------------------|
| `WithPrincipalMember(fn)`     | Override how a Principal is mapped to an IAM member string (its ARN).        |
| `WithExpectedMember(arn)`     | Deny any Principal whose extracted member doesn't equal `arn`.               |
| `WithInitialPermissions(m)`   | Seed the cache with a pre-computed `map[action]bool` before the first refresh.|

## Behaviour

- `Can(p, action, resource)`:
  - If `WithExpectedMember` is set and the extracted principal member
    doesn't match, returns `false` before any cache lookup or live call.
  - If `resource` is `IAMResource{Ctx, Arn}` (or `*IAMResource`) and
    `Arn` is non-empty, dispatches a live
    `iam:SimulatePrincipalPolicy` for that single action and returns
    `EvalDecision == "allowed"`. Errors map to `false`.
  - Otherwise consults the cache: returns `cache[action]`. Actions not
    in the cache return `false`.
- The background loop calls `iam:SimulatePrincipalPolicy` every
  `refresh` interval with `PolicySourceArn = principalArn`,
  `ActionNames = actions`, `ResourceArns = [resource]`. On success the
  cache map is atomically replaced. On error the previous cache is
  preserved.
- `Close` is safe to call multiple times and blocks until the loop
  goroutine has exited.

## IAM permission required

The credentials the client is authenticated with need
`iam:SimulatePrincipalPolicy` for the `PolicySourceArn` being
simulated. See the
[IAM API reference](https://docs.aws.amazon.com/IAM/latest/APIReference/API_SimulatePrincipalPolicy.html)
for details.

## Notes

- `iam:SimulatePrincipalPolicy` evaluates policies attached to the
  given `PolicySourceArn` (an IAM user, group, or role). It does **not**
  evaluate policies of an arbitrary principal at call time —
  `WithExpectedMember` makes that constraint explicit so a Principal
  with a different identity is not silently granted the cached role's
  permissions.
- IAM permission boundaries, session policies, and SCPs are honoured by
  the simulation.
- The cache is invalidated only by `Refresh` (manual or the background
  loop). Rotate the refresh interval to trade freshness for QPS against
  IAM's throttling limits.
