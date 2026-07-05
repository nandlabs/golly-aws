package dynamolock

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Options configures a DynamoDB-backed leader elector.
type Options struct {
	// Table is the DynamoDB table that holds lock items. The table
	// MUST have a single partition key named "lock_id" (type S). See
	// README.md for the recommended schema and TTL setup.
	Table string
	// Key is the partition-key value that identifies THIS lock. All
	// contenders for the same leadership role MUST agree on Table + Key.
	Key string
	// Owner is the identity of THIS instance. It should be globally
	// unique across contenders (hostname+pid+random is a good default).
	Owner string
	// Lease is the renewal cadence. The elector attempts renewal every
	// Lease/2, and the persisted expiry is now + 3*Lease. Choose Lease
	// larger than expected clock skew and GC pauses.
	Lease time.Duration
}

// item column names — kept as constants so the schema is documented in
// one place and easy to keep in sync with README.md.
const (
	attrLockID     = "lock_id"
	attrOwner      = "owner"
	attrLeaseUntil = "lease_until"
	attrVersion    = "version"
)

// ddbBackend abstracts the DynamoDB operations the elector needs so
// tests can substitute an in-memory fake with matching conditional
// semantics.
type ddbBackend interface {
	put(ctx context.Context, in *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error)
	get(ctx context.Context, in *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error)
	del(ctx context.Context, in *dynamodb.DeleteItemInput) (*dynamodb.DeleteItemOutput, error)
}

// Elector implements chrono.LeaderElector on top of a single DynamoDB
// item guarded by conditional writes.
type Elector struct {
	be   ddbBackend
	opts Options
	now  func() time.Time

	leader atomic.Bool
	// heldVersion is the version we last wrote successfully. It's used
	// only on the renewal goroutine and on Resign after the goroutine
	// has exited, so no lock is required.
	heldVersion int64

	startOnce sync.Once
	started   bool
	cancel    context.CancelFunc
	done      chan struct{}
}

// New constructs a DynamoDB-backed Elector using the provided client.
// The elector does not own the client — the caller manages its
// lifecycle.
func New(client *dynamodb.Client, opts Options) *Elector {
	return newWithBackend(&clientBackend{c: client}, opts)
}

// newWithBackend is the internal constructor used by both New and tests.
func newWithBackend(be ddbBackend, opts Options) *Elector {
	return &Elector{
		be:   be,
		opts: opts,
		now:  time.Now,
	}
}

// Start validates configuration and launches the background renewal
// goroutine. Idempotent — repeated calls are no-ops. Start performs one
// synchronous tick before returning so callers who wait briefly can
// observe leadership without racing the ticker.
func (e *Elector) Start(ctx context.Context) error {
	if err := e.validate(); err != nil {
		return err
	}
	e.startOnce.Do(func() {
		loopCtx, cancel := context.WithCancel(context.Background())
		e.cancel = cancel
		e.done = make(chan struct{})
		e.started = true
		_ = e.tick(ctx)
		go e.run(loopCtx)
	})
	return nil
}

// IsLeader reads the atomic leadership flag maintained by the renewal
// loop. It does NOT perform I/O and is safe from hot paths.
func (e *Elector) IsLeader(_ context.Context) bool { return e.leader.Load() }

// Resign stops the renewal loop and, if this instance currently owns
// the lock, deletes the item with a conditional expression so a
// successor that has already stolen the lease is not clobbered.
func (e *Elector) Resign(ctx context.Context) error {
	if !e.started {
		return nil
	}
	if e.cancel != nil {
		e.cancel()
	}
	if e.done != nil {
		<-e.done
	}
	if !e.leader.Load() {
		return nil
	}
	_, err := e.be.del(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(e.opts.Table),
		Key: map[string]ddbtypes.AttributeValue{
			attrLockID: &ddbtypes.AttributeValueMemberS{Value: e.opts.Key},
		},
		ConditionExpression: aws.String("#o = :us"),
		ExpressionAttributeNames: map[string]string{
			"#o": attrOwner,
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":us": &ddbtypes.AttributeValueMemberS{Value: e.opts.Owner},
		},
	})
	e.leader.Store(false)
	e.heldVersion = 0
	if isConditionalCheckFailed(err) {
		// Someone else already stole the lease — resign is a no-op.
		return nil
	}
	return err
}

func (e *Elector) validate() error {
	if e.opts.Table == "" {
		return errors.New("dynamolock: Table required")
	}
	if e.opts.Key == "" {
		return errors.New("dynamolock: Key required")
	}
	if e.opts.Owner == "" {
		return errors.New("dynamolock: Owner required")
	}
	if e.opts.Lease <= 0 {
		return errors.New("dynamolock: Lease must be > 0")
	}
	return nil
}

func (e *Elector) run(ctx context.Context) {
	defer close(e.done)
	interval := e.opts.Lease / 2
	if interval <= 0 {
		interval = e.opts.Lease
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = e.tick(ctx)
		}
	}
}

// tick runs one renewal attempt.
//
//  1. Try a conditional PutItem that only succeeds if the item does
//     not exist, we already own it, or the current lease has expired.
//  2. On ConditionalCheckFailedException, GetItem to inspect the
//     current owner and lease; if a live foreign lease is present we
//     mark ourselves not-leader.
//  3. Transport failures do NOT flip leadership state — the persisted
//     lease_until is the source of truth for other observers.
func (e *Elector) tick(ctx context.Context) error {
	now := e.now()
	expiry := now.Add(3 * e.opts.Lease).Unix()
	nowUnix := now.Unix()
	newVersion := e.heldVersion + 1
	if newVersion <= 0 {
		newVersion = 1
	}

	put := &dynamodb.PutItemInput{
		TableName: aws.String(e.opts.Table),
		Item: map[string]ddbtypes.AttributeValue{
			attrLockID:     &ddbtypes.AttributeValueMemberS{Value: e.opts.Key},
			attrOwner:      &ddbtypes.AttributeValueMemberS{Value: e.opts.Owner},
			attrLeaseUntil: &ddbtypes.AttributeValueMemberN{Value: strconv.FormatInt(expiry, 10)},
			attrVersion:    &ddbtypes.AttributeValueMemberN{Value: strconv.FormatInt(newVersion, 10)},
		},
		ConditionExpression: aws.String("attribute_not_exists(#k) OR #o = :us OR #l < :now"),
		ExpressionAttributeNames: map[string]string{
			"#k": attrLockID,
			"#o": attrOwner,
			"#l": attrLeaseUntil,
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":us":  &ddbtypes.AttributeValueMemberS{Value: e.opts.Owner},
			":now": &ddbtypes.AttributeValueMemberN{Value: strconv.FormatInt(nowUnix, 10)},
		},
	}

	if _, err := e.be.put(ctx, put); err == nil {
		e.heldVersion = newVersion
		e.leader.Store(true)
		return nil
	} else if !isConditionalCheckFailed(err) {
		// Transport-level failure: preserve prior atomic value.
		return err
	}

	// Conditional check failed — inspect the current item to see if
	// someone else holds a live lease.
	out, err := e.be.get(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(e.opts.Table),
		ConsistentRead: aws.Bool(true),
		Key: map[string]ddbtypes.AttributeValue{
			attrLockID: &ddbtypes.AttributeValueMemberS{Value: e.opts.Key},
		},
	})
	if err != nil {
		return err
	}
	owner, leaseUntil, ok := parseItem(out.Item)
	if !ok {
		// Item vanished between the failed PUT and the GET — next tick
		// will try the create path again.
		e.leader.Store(false)
		return nil
	}
	// Per the condition, the PUT can only have failed when neither
	// the item was absent, nor was owner==us, nor was the lease
	// expired. So a foreign owner MUST be holding a live lease. We
	// still guard on the observed values to stay correct under a
	// concurrent successor that stole and renewed during our GetItem.
	if owner != e.opts.Owner || leaseUntil > nowUnix {
		e.leader.Store(false)
	}
	return nil
}

// parseItem extracts owner and lease_until from a DynamoDB item. It
// returns ok=false when the item is nil or missing required fields, so
// corrupt items are treated as "no lease held".
func parseItem(item map[string]ddbtypes.AttributeValue) (owner string, leaseUntil int64, ok bool) {
	if item == nil {
		return "", 0, false
	}
	oav, ok1 := item[attrOwner].(*ddbtypes.AttributeValueMemberS)
	lav, ok2 := item[attrLeaseUntil].(*ddbtypes.AttributeValueMemberN)
	if !ok1 || !ok2 {
		return "", 0, false
	}
	lu, err := strconv.ParseInt(lav.Value, 10, 64)
	if err != nil {
		return "", 0, false
	}
	return oav.Value, lu, true
}

// isConditionalCheckFailed unwraps a DynamoDB error and reports whether
// it is a ConditionalCheckFailedException.
func isConditionalCheckFailed(err error) bool {
	if err == nil {
		return false
	}
	var cf *ddbtypes.ConditionalCheckFailedException
	return errors.As(err, &cf)
}

// clientBackend is the production backend that dispatches directly to
// the DynamoDB client.
type clientBackend struct{ c *dynamodb.Client }

func (b *clientBackend) put(ctx context.Context, in *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
	return b.c.PutItem(ctx, in)
}
func (b *clientBackend) get(ctx context.Context, in *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
	return b.c.GetItem(ctx, in)
}
func (b *clientBackend) del(ctx context.Context, in *dynamodb.DeleteItemInput) (*dynamodb.DeleteItemOutput, error) {
	return b.c.DeleteItem(ctx, in)
}
