package dynamolock

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeBackend is an in-memory substitute for the DynamoDB client with
// matching conditional-write semantics on a single item keyed by
// (Table, Key). All operations acquire the same mutex so the outcome
// matches DynamoDB's strong-consistency guarantee for conditional writes
// against a single item.
type fakeBackend struct {
	mu       sync.Mutex
	item     map[string]ddbtypes.AttributeValue // nil == absent
	calls    atomic.Int64
	failNext atomic.Int64
}

var errInjected = errors.New("injected transport error")

func newFakeBackend() *fakeBackend { return &fakeBackend{} }

func (b *fakeBackend) put(ctx context.Context, in *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
	b.calls.Add(1)
	if n := b.failNext.Load(); n > 0 {
		b.failNext.Add(-1)
		return nil, errInjected
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.evalCondition(in) {
		return nil, &ddbtypes.ConditionalCheckFailedException{}
	}
	// Copy the item so callers can't mutate our storage.
	b.item = cloneItem(in.Item)
	return &dynamodb.PutItemOutput{}, nil
}

func (b *fakeBackend) get(ctx context.Context, in *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
	b.calls.Add(1)
	if n := b.failNext.Load(); n > 0 {
		b.failNext.Add(-1)
		return nil, errInjected
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.item == nil {
		return &dynamodb.GetItemOutput{}, nil
	}
	return &dynamodb.GetItemOutput{Item: cloneItem(b.item)}, nil
}

func (b *fakeBackend) del(ctx context.Context, in *dynamodb.DeleteItemInput) (*dynamodb.DeleteItemOutput, error) {
	b.calls.Add(1)
	if n := b.failNext.Load(); n > 0 {
		b.failNext.Add(-1)
		return nil, errInjected
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.item == nil {
		// A delete of an absent item satisfies the "#o = :us" condition
		// vacuously in our tests only if we're being asked WITH the
		// condition; without a condition, it's a no-op. DynamoDB's real
		// behavior differs — it treats attribute_exists as required for
		// the condition to hold — so we mirror that here.
		if in.ConditionExpression != nil {
			return nil, &ddbtypes.ConditionalCheckFailedException{}
		}
		return &dynamodb.DeleteItemOutput{}, nil
	}
	if in.ConditionExpression != nil {
		// The elector only issues "#o = :us" so we evaluate that.
		wantOwner := extractS(in.ExpressionAttributeValues, ":us")
		haveOwner := extractS(b.item, attrOwner)
		if wantOwner != haveOwner {
			return nil, &ddbtypes.ConditionalCheckFailedException{}
		}
	}
	b.item = nil
	return &dynamodb.DeleteItemOutput{}, nil
}

// evalCondition evaluates the elector's PutItem condition
// "attribute_not_exists(#k) OR #o = :us OR #l < :now" against the fake's
// current state. It's a minimal expression evaluator scoped to the
// exact expression the elector emits.
func (b *fakeBackend) evalCondition(in *dynamodb.PutItemInput) bool {
	if in.ConditionExpression == nil {
		return true
	}
	if b.item == nil {
		// attribute_not_exists(#k)
		return true
	}
	wantOwner := extractS(in.ExpressionAttributeValues, ":us")
	haveOwner := extractS(b.item, attrOwner)
	if wantOwner == haveOwner {
		return true
	}
	nowStr := extractN(in.ExpressionAttributeValues, ":now")
	leaseStr := extractN(b.item, attrLeaseUntil)
	nowV, _ := strconv.ParseInt(nowStr, 10, 64)
	leaseV, _ := strconv.ParseInt(leaseStr, 10, 64)
	return leaseV < nowV
}

func extractS(m map[string]ddbtypes.AttributeValue, k string) string {
	av, ok := m[k].(*ddbtypes.AttributeValueMemberS)
	if !ok {
		return ""
	}
	return av.Value
}

func extractN(m map[string]ddbtypes.AttributeValue, k string) string {
	av, ok := m[k].(*ddbtypes.AttributeValueMemberN)
	if !ok {
		return ""
	}
	return av.Value
}

func cloneItem(m map[string]ddbtypes.AttributeValue) map[string]ddbtypes.AttributeValue {
	out := make(map[string]ddbtypes.AttributeValue, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// snapshot returns the fake's current item (nil if absent).
func (b *fakeBackend) snapshot() map[string]ddbtypes.AttributeValue {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.item == nil {
		return nil
	}
	return cloneItem(b.item)
}

// mockClock is a monotonic advancer for deterministic tick tests.
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

func newTestElector(be ddbBackend, owner string, clock *mockClock) *Elector {
	e := newWithBackend(be, Options{
		Table: "locks",
		Key:   "sched",
		Owner: owner,
		Lease: 10 * time.Second,
	})
	e.now = clock.Now
	return e
}

func ownerOf(item map[string]ddbtypes.AttributeValue) string {
	return extractS(item, attrOwner)
}

func leaseOf(item map[string]ddbtypes.AttributeValue) int64 {
	v, _ := strconv.ParseInt(extractN(item, attrLeaseUntil), 10, 64)
	return v
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
	st := be.snapshot()
	if ownerOf(st) != "instance-a" {
		t.Fatalf("owner = %q, want instance-a", ownerOf(st))
	}
	if want := clock.Now().Add(30 * time.Second).Unix(); leaseOf(st) != want {
		t.Fatalf("lease_until = %d, want %d", leaseOf(st), want)
	}
}

func TestRenew_RetainsLeadershipAcrossLoops(t *testing.T) {
	be := newFakeBackend()
	clock := newMockClock(time.Unix(1_700_000_000, 0))
	e := newTestElector(be, "instance-a", clock)

	if err := e.tick(context.Background()); err != nil {
		t.Fatalf("tick1: %v", err)
	}
	first := leaseOf(be.snapshot())

	clock.Advance(4 * time.Second)
	if err := e.tick(context.Background()); err != nil {
		t.Fatalf("tick2: %v", err)
	}
	if !e.IsLeader(context.Background()) {
		t.Fatalf("expected still leader after renewal")
	}
	second := leaseOf(be.snapshot())
	if second <= first {
		t.Fatalf("lease_until did not advance: first=%d second=%d", first, second)
	}
	if ownerOf(be.snapshot()) != "instance-a" {
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
	if ownerOf(be.snapshot()) != "instance-b" {
		t.Fatalf("owner should be challenger after steal, got %q", ownerOf(be.snapshot()))
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
	if ownerOf(be.snapshot()) != "instance-a" {
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
	if be.snapshot() != nil {
		t.Fatalf("expected item cleared, got %+v", be.snapshot())
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
	if ownerOf(be.snapshot()) != "instance-a" {
		t.Fatalf("b Resign clobbered a's ownership: %+v", be.snapshot())
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

func (b *blockingBackend) put(ctx context.Context, in *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.ch:
		return &dynamodb.PutItemOutput{}, nil
	}
}
func (b *blockingBackend) get(ctx context.Context, in *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.ch:
		return &dynamodb.GetItemOutput{}, nil
	}
}
func (b *blockingBackend) del(ctx context.Context, in *dynamodb.DeleteItemInput) (*dynamodb.DeleteItemOutput, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.ch:
		return &dynamodb.DeleteItemOutput{}, nil
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
		"no table": {Key: "k", Owner: "o", Lease: time.Second},
		"no key":   {Table: "t", Owner: "o", Lease: time.Second},
		"no owner": {Table: "t", Key: "k", Lease: time.Second},
		"no lease": {Table: "t", Key: "k", Owner: "o"},
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
