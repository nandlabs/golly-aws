# dynamolock

A `chrono.LeaderElector` backed by a single DynamoDB item. Ownership is
coordinated by conditional `PutItem` writes: every renewal tick submits
one write that succeeds only if the item is absent, already belongs to
this owner, or has an expired lease. DynamoDB conditional writes are
strongly consistent on a single item, so exactly one contender wins any
contested transition.

## Quick start

```go
import (
    "github.com/aws/aws-sdk-go-v2/service/dynamodb"
    "oss.nandlabs.io/golly-aws/chrono/dynamolock"
    "oss.nandlabs.io/golly/chrono"
)

client := dynamodb.NewFromConfig(cfg)
elector := dynamolock.New(client, dynamolock.Options{
    Table: "chrono-locks",
    Key:   "scheduler",
    Owner: hostnamePidRandom(),
    Lease: 15 * time.Second,
})
_ = elector.Start(ctx)
defer elector.Resign(context.Background())

sched := chrono.NewScheduler(chrono.WithLeaderElector(elector))
```

`IsLeader` reads a local atomic — safe to call in hot paths.

## Table schema

Create the table with a single partition key. Enable TTL on
`lease_until` so DynamoDB eventually cleans up items no live contender
is renewing.

| Field         | Type                     | Notes                                  |
| ------------- | ------------------------ | -------------------------------------- |
| `lock_id`     | String (partition key)   | Value equals `Options.Key`.            |
| `owner`       | String                   | Current holder's identity.             |
| `lease_until` | Number (unix seconds)    | Enable **DynamoDB TTL** on this attr.  |
| `version`     | Number                   | Monotonic; useful for diagnostics.     |

**Clock skew:** the lease is an absolute wall-clock deadline, so
instance clocks SHOULD stay within `Lease/2` of one another (NTP/chrony
with the default sync interval is sufficient).
