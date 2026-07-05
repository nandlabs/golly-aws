// Package s3lock provides a chrono.LeaderElector backed by a single
// S3 object. Leadership is coordinated by S3's write preconditions:
// PutObject with If-None-Match:"*" for the first claim, and PutObject
// with If-Match:<etag> for renew, steal, and resign.
//
// S3 has provided strongly consistent read-after-write since December
// 2020 and added conditional writes via If-None-Match in August 2024,
// so exactly one contender wins any contested transition — the losers
// receive HTTP 412 PreconditionFailed and observe the winner's state on
// their next tick.
//
// The elector runs a background renewal goroutine at Lease/2 cadence.
// The persisted body is a small JSON blob carrying {owner, lease_until}.
// IsLeader reads a local atomic — no I/O — so it is safe from hot paths.
//
// Clock skew: lease_until is a wall-clock timestamp, so instances SHOULD
// keep clock skew below Lease/2 for correctness.
package s3lock
