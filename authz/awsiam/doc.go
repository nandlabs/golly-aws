// Package awsiam adapts AWS IAM permission checks to the
// oss.nandlabs.io/golly/authz.Policy interface.
//
// The upstream Policy interface has the signature
//
//	Can(p Principal, action string, resource any) bool
//
// which is context-free and returns a plain bool. That does not fit the
// shape of an AWS IAM call (which needs a context.Context and can fail
// with a network error). This package offers two workable shapes:
//
//   - A cached Evaluator that refreshes an allowed-actions map on a
//     background goroutine. Can consults the cached map synchronously.
//     Freshness is bounded by the configured refresh interval. This is
//     the recommended production shape.
//
//   - An IAMResource envelope callers pass as the `resource any` argument
//     when they want a live iam:SimulatePrincipalPolicy call instead of
//     the cache. The context and the fully-qualified resource ARN ride
//     along on the envelope so Can can type-assert them out.
//
// Under the hood the evaluator calls
// iam.Client.SimulatePrincipalPolicy(ctx, {PolicySourceArn, ActionNames,
// ResourceArns}). An EvalDecision of "allowed" maps to true; anything
// else (explicitDeny, implicitDeny, or a transport error on the live
// path) maps to false.
package awsiam
