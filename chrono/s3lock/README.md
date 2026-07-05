# s3lock

A `chrono.LeaderElector` backed by a single S3 object. Ownership is
coordinated by S3's write preconditions: `PutObject` with
`If-None-Match: "*"` for the first claim, and `PutObject` with
`If-Match: <ETag>` for renewals, steals, and resigns.

## Quick start

```go
import (
    "github.com/aws/aws-sdk-go-v2/service/s3"
    "oss.nandlabs.io/golly-aws/chrono/s3lock"
    "oss.nandlabs.io/golly/chrono"
)

client := s3.NewFromConfig(cfg)
elector := s3lock.New(client, s3lock.Options{
    Bucket: "my-locks",
    Key:    "chrono/scheduler.lock",
    Owner:  hostnamePidRandom(),
    Lease:  15 * time.Second,
})
_ = elector.Start(ctx)
defer elector.Resign(context.Background())

sched := chrono.NewScheduler(chrono.WithLeaderElector(elector))
```

`IsLeader` reads a local atomic — safe to call in hot paths.

## Requirements

- **SDK version**: S3 conditional writes (`If-None-Match: "*"`) landed
  in AWS on 20 Aug 2024 and require an SDK v2 release that surfaces
  the `PutObjectInput.IfNoneMatch` field. This module's pinned
  `github.com/aws/aws-sdk-go-v2/service/s3` version is post-Aug 2024
  and does.
- **Bucket must exist**. The elector does not create it.
- **Clock skew**: the lease is an absolute wall-clock deadline, so
  instance clocks SHOULD stay within `Lease/2` of one another
  (NTP/chrony with the default sync interval is sufficient).
