package s3lock

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// newTestClient dials either MinIO / other S3-compatible service at
// S3_ENDPOINT or the caller's default AWS environment. The test is
// skipped unless S3_TEST_BUCKET is set (both real AWS and MinIO
// require the bucket to be pre-created — the elector does not create
// buckets).
func newTestClient(t *testing.T) (*s3.Client, string) {
	t.Helper()
	bucket := os.Getenv("S3_TEST_BUCKET")
	if bucket == "" {
		t.Skip("S3_TEST_BUCKET not set; skipping S3 smoke test")
	}
	ctx := context.Background()
	opts := []func(*config.LoadOptions) error{}
	if r := os.Getenv("AWS_REGION"); r != "" {
		opts = append(opts, config.WithRegion(r))
	} else {
		opts = append(opts, config.WithRegion("us-east-1"))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		t.Fatalf("config.LoadDefaultConfig: %v", err)
	}
	var clientOpts []func(*s3.Options)
	if ep := os.Getenv("S3_ENDPOINT"); ep != "" {
		clientOpts = append(clientOpts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(ep)
			o.UsePathStyle = true
		})
	}
	return s3.NewFromConfig(cfg, clientOpts...), bucket
}

func uniqueKey(t *testing.T) string {
	return fmt.Sprintf("chrono-locks/%s-%d.json", t.Name(), time.Now().UnixNano())
}

func TestSmoke_ClaimAndRenew(t *testing.T) {
	client, bucket := newTestClient(t)
	ctx := context.Background()

	opts := Options{
		Bucket: bucket,
		Key:    uniqueKey(t),
		Owner:  "instance-a",
		Lease:  500 * time.Millisecond,
	}
	e := New(client, opts)
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(750 * time.Millisecond)
	if !e.IsLeader(ctx) {
		t.Fatalf("expected leader after smoke tick")
	}
	if err := e.Resign(ctx); err != nil {
		t.Fatalf("Resign: %v", err)
	}
}

func TestSmoke_SecondInstanceLoses(t *testing.T) {
	client, bucket := newTestClient(t)
	ctx := context.Background()
	key := uniqueKey(t)

	a := New(client, Options{Bucket: bucket, Key: key, Owner: "a", Lease: 500 * time.Millisecond})
	b := New(client, Options{Bucket: bucket, Key: key, Owner: "b", Lease: 500 * time.Millisecond})

	if err := a.Start(ctx); err != nil {
		t.Fatalf("a.Start: %v", err)
	}
	if err := b.Start(ctx); err != nil {
		t.Fatalf("b.Start: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if !a.IsLeader(ctx) {
		t.Fatalf("expected a to be leader")
	}
	if b.IsLeader(ctx) {
		t.Fatalf("expected b to NOT be leader")
	}
	_ = a.Resign(ctx)
	_ = b.Resign(ctx)
}
