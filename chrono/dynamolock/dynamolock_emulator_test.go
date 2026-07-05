package dynamolock

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// newTestClient dials either a local dynamodb-local at DYNAMODB_ENDPOINT
// or the caller's default AWS environment. The test is skipped unless
// DYNAMODB_TEST_TABLE is set (both real AWS and local emulator require
// the table to be pre-created — the elector does not create it).
func newTestClient(t *testing.T) (*dynamodb.Client, string) {
	t.Helper()
	table := os.Getenv("DYNAMODB_TEST_TABLE")
	if table == "" {
		t.Skip("DYNAMODB_TEST_TABLE not set; skipping DynamoDB smoke test")
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
	var clientOpts []func(*dynamodb.Options)
	if ep := os.Getenv("DYNAMODB_ENDPOINT"); ep != "" {
		clientOpts = append(clientOpts, func(o *dynamodb.Options) {
			o.BaseEndpoint = aws.String(ep)
		})
	}
	return dynamodb.NewFromConfig(cfg, clientOpts...), table
}

func uniqueKey(t *testing.T) string {
	return fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
}

func TestSmoke_ClaimAndRenew(t *testing.T) {
	client, table := newTestClient(t)
	ctx := context.Background()

	opts := Options{
		Table: table,
		Key:   uniqueKey(t),
		Owner: "instance-a",
		Lease: 500 * time.Millisecond,
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
	client, table := newTestClient(t)
	ctx := context.Background()
	key := uniqueKey(t)

	a := New(client, Options{Table: table, Key: key, Owner: "a", Lease: 500 * time.Millisecond})
	b := New(client, Options{Table: table, Key: key, Owner: "b", Lease: 500 * time.Millisecond})

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
