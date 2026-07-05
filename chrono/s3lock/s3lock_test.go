package s3lock

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// fakeBackend is an in-memory substitute for the S3 client with
// matching If-Match / If-None-Match precondition semantics on a single
// object slot. Every mutating operation advances the ETag; every
// precondition check must match the current ETag exactly.
type fakeBackend struct {
	mu       sync.Mutex
	exists   bool
	body     []byte
	etag     string
	nextGen  int64
	calls    atomic.Int64
	failNext atomic.Int64
}

var errInjected = errors.New("injected transport error")

func newFakeBackend() *fakeBackend { return &fakeBackend{nextGen: 100} }

func (b *fakeBackend) advanceETag() {
	b.nextGen++
	b.etag = fmt.Sprintf("\"etag-%d\"", b.nextGen)
}

func (b *fakeBackend) getObject(ctx context.Context, in *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
	b.calls.Add(1)
	if n := b.failNext.Load(); n > 0 {
		b.failNext.Add(-1)
		return nil, errInjected
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.exists {
		return nil, &s3types.NoSuchKey{}
	}
	buf := make([]byte, len(b.body))
	copy(buf, b.body)
	return &s3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader(buf)),
		ETag: aws.String(b.etag),
	}, nil
}

func (b *fakeBackend) putObject(ctx context.Context, in *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
	b.calls.Add(1)
	if n := b.failNext.Load(); n > 0 {
		b.failNext.Add(-1)
		return nil, errInjected
	}
	body, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	// If-None-Match: "*" requires the object to be absent.
	if in.IfNoneMatch != nil && *in.IfNoneMatch == "*" {
		if b.exists {
			return nil, errPreconditionFailed
		}
	}
	// If-Match: <etag> requires exact ETag match.
	if in.IfMatch != nil {
		if !b.exists || b.etag != *in.IfMatch {
			return nil, errPreconditionFailed
		}
	}
	b.exists = true
	b.body = append([]byte(nil), body...)
	b.advanceETag()
	return &s3.PutObjectOutput{ETag: aws.String(b.etag)}, nil
}

func (b *fakeBackend) deleteObject(ctx context.Context, in *s3.DeleteObjectInput) (*s3.DeleteObjectOutput, error) {
	b.calls.Add(1)
	if n := b.failNext.Load(); n > 0 {
		b.failNext.Add(-1)
		return nil, errInjected
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.exists {
		// A delete of an absent key is a no-op — S3 returns 204.
		return &s3.DeleteObjectOutput{}, nil
	}
	if in.IfMatch != nil && b.etag != *in.IfMatch {
		return nil, errPreconditionFailed
	}
	b.exists = false
	b.body = nil
	b.advanceETag()
	return &s3.DeleteObjectOutput{}, nil
}

// snapshot returns a copy of the fake's stored state.
func (b *fakeBackend) snapshot() (exists bool, body []byte, etag string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.exists {
		return false, nil, b.etag
	}
	buf := make([]byte, len(b.body))
	copy(buf, b.body)
	return true, buf, b.etag
}

type mockClock struct {
	mu  sync.Mutex
	now time.Time
}

func newMockClock(t time.Time) *mockClock { return &mockClock{now: t} }

func (c *mockClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mockClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestElector(be s3Backend, owner string, clock *mockClock) *Elector {
	e := newWithBackend(be, Options{
		Bucket: "test-bucket",
		Key:    "chrono/sched.lock",
		Owner:  owner,
		Lease:  10 * time.Second,
	})
	e.now = clock.Now
	return e
}

func TestClaim_FirstAcquirer(t *testing.T) {
	be := newFakeBackend()
	clock := newMockClock(time.Unix(1_700_000_000, 0))
	e := newTestElector(be, "instance-a", clock)

	if err := e.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !e.IsLeader(context.Background()) {
		t.Fatalf("expected leader after first tick")
	}
	exists, body, _ := be.snapshot()
	if !exists {
		t.Fatalf("expected object created")
	}
	doc := parseDoc(body)
	if doc.Owner != "instance-a" {
		t.Fatalf("owner = %q, want instance-a", doc.Owner)
	}
	if want := clock.Now().Add(30 * time.Second); !doc.LeaseUntil.Equal(want) {
		t.Fatalf("lease_until = %v, want %v", doc.LeaseUntil, want)
	}
}

func TestRenew_RetainsLeadershipAcrossLoops(t *testing.T) {
	be := newFakeBackend()
	clock := newMockClock(time.Unix(1_700_000_000, 0))
	e := newTestElector(be, "instance-a", clock)

	if err := e.tick(context.Background()); err != nil {
		t.Fatalf("tick1: %v", err)
	}
	_, body1, tag1 := be.snapshot()
	first := parseDoc(body1).LeaseUntil

	clock.Advance(4 * time.Second)
	if err := e.tick(context.Background()); err != nil {
		t.Fatalf("tick2: %v", err)
	}
	if !e.IsLeader(context.Background()) {
		t.Fatalf("expected still leader after renewal")
	}
	_, body2, tag2 := be.snapshot()
	second := parseDoc(body2).LeaseUntil
	if !second.After(first) {
		t.Fatalf("lease_until did not advance: first=%v second=%v", first, second)
	}
	if tag2 == tag1 {
		t.Fatalf("ETag did not advance across renewals")
	}
	if parseDoc(body2).Owner != "instance-a" {
		t.Fatalf("owner drifted after renew")
	}
}

func TestSteal_AfterLeaseExpiry(t *testing.T) {
	be := newFakeBackend()
	clock := newMockClock(time.Unix(1_700_000_000, 0))

	incumbent := newTestElector(be, "instance-a", clock)
	if err := incumbent.tick(context.Background()); err != nil {
		t.Fatalf("incumbent tick: %v", err)
	}
	clock.Advance(31 * time.Second)

	challenger := newTestElector(be, "instance-b", clock)
	if err := challenger.tick(context.Background()); err != nil {
		t.Fatalf("challenger tick: %v", err)
	}
	if !challenger.IsLeader(context.Background()) {
		t.Fatalf("challenger should have stolen expired lease")
	}
	_, body, _ := be.snapshot()
	if parseDoc(body).Owner != "instance-b" {
		t.Fatalf("owner should be challenger after steal")
	}
}

func TestSecondInstance_NotLeader_WhileFirstAlive(t *testing.T) {
	be := newFakeBackend()
	clock := newMockClock(time.Unix(1_700_000_000, 0))

	a := newTestElector(be, "instance-a", clock)
	b := newTestElector(be, "instance-b", clock)

	if err := a.tick(context.Background()); err != nil {
		t.Fatalf("a tick: %v", err)
	}
	clock.Advance(2 * time.Second)
	if err := b.tick(context.Background()); err != nil {
		t.Fatalf("b tick: %v", err)
	}
	if !a.IsLeader(context.Background()) {
		t.Fatalf("a should still be leader")
	}
	if b.IsLeader(context.Background()) {
		t.Fatalf("b should NOT be leader")
	}
	_, body, _ := be.snapshot()
	if parseDoc(body).Owner != "instance-a" {
		t.Fatalf("stored owner should remain instance-a")
	}
}

func TestResign_ClearsRecord_WhenLeader(t *testing.T) {
	be := newFakeBackend()
	clock := newMockClock(time.Unix(1_700_000_000, 0))
	e := newTestElector(be, "instance-a", clock)

	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !e.IsLeader(context.Background()) {
		t.Fatalf("expected leader after Start")
	}
	if err := e.Resign(context.Background()); err != nil {
		t.Fatalf("Resign: %v", err)
	}
	if e.IsLeader(context.Background()) {
		t.Fatalf("expected NOT leader after Resign")
	}
	exists, _, _ := be.snapshot()
	if exists {
		t.Fatalf("expected object cleared on Resign")
	}
}

func TestResign_NoOp_WhenNotLeader(t *testing.T) {
	be := newFakeBackend()
	clock := newMockClock(time.Unix(1_700_000_000, 0))

	a := newTestElector(be, "instance-a", clock)
	b := newTestElector(be, "instance-b", clock)

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("a Start: %v", err)
	}
	if err := b.Start(context.Background()); err != nil {
		t.Fatalf("b Start: %v", err)
	}
	if !a.IsLeader(context.Background()) || b.IsLeader(context.Background()) {
		t.Fatalf("expected a leader, b not — got a=%v b=%v", a.IsLeader(context.Background()), b.IsLeader(context.Background()))
	}
	if err := b.Resign(context.Background()); err != nil {
		t.Fatalf("b Resign: %v", err)
	}
	exists, body, _ := be.snapshot()
	if !exists || parseDoc(body).Owner != "instance-a" {
		t.Fatalf("b Resign clobbered a's ownership: exists=%v body=%q", exists, string(body))
	}
	if err := a.Resign(context.Background()); err != nil {
		t.Fatalf("a Resign: %v", err)
	}
}

func TestIsLeader_ReturnsAtomicallyFast(t *testing.T) {
	be := &blockingBackend{ch: make(chan struct{})}
	defer close(be.ch)
	clock := newMockClock(time.Unix(1_700_000_000, 0))
	e := newTestElector(be, "instance-a", clock)
	e.leader.Store(true)

	done := make(chan bool, 1)
	start := time.Now()
	go func() { done <- e.IsLeader(context.Background()) }()
	select {
	case v := <-done:
		if !v {
			t.Fatalf("IsLeader returned false, want true")
		}
		if time.Since(start) > 100*time.Millisecond {
			t.Fatalf("IsLeader took too long — likely blocked on I/O")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("IsLeader did not return within 100ms — likely blocked on I/O")
	}
}

// blockingBackend blocks every backend call until ch closes.
type blockingBackend struct{ ch chan struct{} }

func (b *blockingBackend) getObject(ctx context.Context, in *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.ch:
		return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(nil))}, nil
	}
}
func (b *blockingBackend) putObject(ctx context.Context, in *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.ch:
		return &s3.PutObjectOutput{}, nil
	}
}
func (b *blockingBackend) deleteObject(ctx context.Context, in *s3.DeleteObjectInput) (*s3.DeleteObjectOutput, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.ch:
		return &s3.DeleteObjectOutput{}, nil
	}
}

func TestStart_IsIdempotent(t *testing.T) {
	be := newFakeBackend()
	clock := newMockClock(time.Unix(1_700_000_000, 0))
	e := newTestElector(be, "instance-a", clock)
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	_ = e.Resign(context.Background())
}

func TestValidate_RejectsBadOptions(t *testing.T) {
	be := newFakeBackend()
	cases := map[string]Options{
		"no bucket": {Key: "x", Owner: "y", Lease: time.Second},
		"no key":    {Bucket: "b", Owner: "y", Lease: time.Second},
		"no owner":  {Bucket: "b", Key: "x", Lease: time.Second},
		"no lease":  {Bucket: "b", Key: "x", Owner: "y"},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			e := newWithBackend(be, opts)
			if err := e.Start(context.Background()); err == nil {
				t.Fatalf("expected validation error for %s", name)
			}
		})
	}
}
