// Package dynamolock provides a chrono.LeaderElector backed by a single
// DynamoDB item. Leadership is coordinated by conditional PutItem calls:
// the elector claims/renews the lock only when either no live lease
// exists (attribute_not_exists OR lease_until < now) or the item already
// belongs to this owner. DynamoDB conditional writes are strongly
// consistent, so at most one contender can commit a given transition.
//
// The elector runs a background renewal goroutine at Lease/2 cadence.
// The persisted item carries {owner, lease_until, version}; lease_until
// is stored as a Unix seconds number so it doubles as a DynamoDB TTL
// attribute — enabling TTL on the table lets DynamoDB eventually garbage
// collect stale items no live contender is renewing.
//
// The public IsLeader call performs no I/O — it reads a local atomic
// updated by the renewal loop — so it is safe from hot paths (e.g.
// per-tick from the scheduler).
//
// Clock skew: because the lock is expressed as an absolute wall-clock
// deadline (lease_until), instances SHOULD keep clock skew below Lease/2
// for correctness. NTP/chrony with a modest sync interval is sufficient.
package dynamolock
