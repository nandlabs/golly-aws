package s3lock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// Options configures an S3-backed leader elector.
type Options struct {
	// Bucket is the S3 bucket that holds the lock object. The bucket
	// must exist prior to Start; the elector does not create buckets.
	Bucket string
	// Key is the object path within the bucket (for example
	// "locks/scheduler.lock"). All contenders for the same leadership
	// role MUST agree on Bucket + Key.
	Key string
	// Owner uniquely identifies THIS instance. Hostname+pid+random is
	// a reasonable default.
	Owner string
	// Lease is the renewal cadence. Renewal fires every Lease/2; the
	// persisted expiry is now + 3*Lease. Choose Lease larger than
	// expected clock skew and GC pauses.
	Lease time.Duration
}

// lockDoc is the JSON body persisted in the lock object.
type lockDoc struct {
	Owner      string    `json:"owner"`
	LeaseUntil time.Time `json:"lease_until"`
}

// s3Backend abstracts the S3 operations the elector needs so tests can
// swap in an in-memory fake with matching precondition semantics.
type s3Backend interface {
	getObject(ctx context.Context, in *s3.GetObjectInput) (*s3.GetObjectOutput, error)
	putObject(ctx context.Context, in *s3.PutObjectInput) (*s3.PutObjectOutput, error)
	deleteObject(ctx context.Context, in *s3.DeleteObjectInput) (*s3.DeleteObjectOutput, error)
}

// errPreconditionFailed is the canonical sentinel returned by backend
// methods when an IfMatch / IfNoneMatch condition fails.
var errPreconditionFailed = errors.New("s3lock: precondition failed")

// Elector implements chrono.LeaderElector on top of an S3 object
// guarded by write preconditions.
type Elector struct {
	be   s3Backend
	opts Options
	now  func() time.Time

	leader atomic.Bool
	// heldETag is the ETag returned by the last successful PUT. It's
	// used for renew (IfMatch) and Resign (IfMatch). Only accessed
	// from the renewal goroutine and from Resign after the goroutine
	// has exited, so no lock is needed.
	heldETag string

	startOnce sync.Once
	started   bool
	cancel    context.CancelFunc
	done      chan struct{}
}

// New constructs an S3-backed Elector using the provided client. The
// elector does not own the client — the caller manages its lifecycle.
func New(client *s3.Client, opts Options) *Elector {
	return newWithBackend(&clientBackend{c: client}, opts)
}

// newWithBackend is the internal constructor used by both New and tests.
func newWithBackend(be s3Backend, opts Options) *Elector {
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

// Resign stops the renewal loop and, if this instance owns the lock,
// deletes the object with an IfMatch precondition — that way a
// successor which has already stolen the lease keeps its object.
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
	if !e.leader.Load() || e.heldETag == "" {
		return nil
	}
	_, err := e.be.deleteObject(ctx, &s3.DeleteObjectInput{
		Bucket:  aws.String(e.opts.Bucket),
		Key:     aws.String(e.opts.Key),
		IfMatch: aws.String(e.heldETag),
	})
	e.leader.Store(false)
	e.heldETag = ""
	if errors.Is(err, errPreconditionFailed) {
		// Someone else already stole/wrote — that's fine on resign.
		return nil
	}
	// A missing key on Resign is not an error either.
	if isNoSuchKey(err) {
		return nil
	}
	return err
}

func (e *Elector) validate() error {
	if e.opts.Bucket == "" {
		return errors.New("s3lock: Bucket required")
	}
	if e.opts.Key == "" {
		return errors.New("s3lock: Key required")
	}
	if e.opts.Owner == "" {
		return errors.New("s3lock: Owner required")
	}
	if e.opts.Lease <= 0 {
		return errors.New("s3lock: Lease must be > 0")
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
//  1. Try PutObject with If-None-Match:"*" to claim when the key is
//     absent.
//  2. On precondition failure, read the current object and inspect
//     owner + lease_until.
//  3. If we own it (renew) or it's expired (steal), PutObject with
//     If-Match:<currentETag> — atomically overwriting the version we
//     just read.
//  4. Otherwise mark not-leader.
func (e *Elector) tick(ctx context.Context) error {
	now := e.now()
	body, err := marshalDoc(lockDoc{Owner: e.opts.Owner, LeaseUntil: now.Add(3 * e.opts.Lease)})
	if err != nil {
		return err
	}

	// (1) First-claim path.
	out, err := e.be.putObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(e.opts.Bucket),
		Key:         aws.String(e.opts.Key),
		Body:        bytes.NewReader(body),
		IfNoneMatch: aws.String("*"),
	})
	if err == nil {
		e.heldETag = derefETag(out.ETag)
		e.leader.Store(true)
		return nil
	}
	if !errors.Is(err, errPreconditionFailed) {
		// Transport-level failure: preserve prior atomic value.
		return err
	}

	// (2) Object exists — read to inspect owner + lease.
	getOut, err := e.be.getObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(e.opts.Bucket),
		Key:    aws.String(e.opts.Key),
	})
	if isNoSuchKey(err) {
		// Rare race: someone deleted between our PUT attempt and the
		// GET. Next tick will retry the create path.
		e.leader.Store(false)
		return nil
	}
	if err != nil {
		return err
	}
	rawBody, curETag, err := readGet(getOut)
	if err != nil {
		return err
	}
	doc := parseDoc(rawBody)

	// (3) Renew (we already hold it) or steal (expired).
	if doc.Owner == e.opts.Owner || !doc.LeaseUntil.After(now) {
		putOut, werr := e.be.putObject(ctx, &s3.PutObjectInput{
			Bucket:  aws.String(e.opts.Bucket),
			Key:     aws.String(e.opts.Key),
			Body:    bytes.NewReader(body),
			IfMatch: aws.String(curETag),
		})
		if werr != nil {
			if errors.Is(werr, errPreconditionFailed) {
				// Another instance beat us to the same write.
				e.leader.Store(false)
				return nil
			}
			return werr
		}
		e.heldETag = derefETag(putOut.ETag)
		e.leader.Store(true)
		return nil
	}
	// (4) Someone else holds a live lease.
	e.leader.Store(false)
	return nil
}

// marshalDoc serialises lockDoc to JSON.
func marshalDoc(d lockDoc) ([]byte, error) {
	b, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("s3lock: marshal lockDoc: %w", err)
	}
	return b, nil
}

// parseDoc decodes a stored body into a lockDoc. Corrupt bodies are
// treated as an expired lease so the elector can steal them cleanly.
func parseDoc(body []byte) lockDoc {
	var d lockDoc
	if err := json.Unmarshal(body, &d); err != nil {
		return lockDoc{}
	}
	return d
}

// readGet consumes a GetObject response body and returns (body, etag).
func readGet(out *s3.GetObjectOutput) ([]byte, string, error) {
	defer func() { _ = out.Body.Close() }()
	body, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, "", err
	}
	return body, derefETag(out.ETag), nil
}

// derefETag returns the raw ETag string or "" for a nil pointer. S3
// ETags are wrapped in double quotes; both IfMatch and IfNoneMatch
// accept the quoted form so we don't strip them here.
func derefETag(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// isPreconditionFailedAPIError reports whether err is an S3
// PreconditionFailed API error (HTTP 412). S3's SDK does not expose a
// dedicated Go type for this response, so we detect it via the
// smithy.APIError code.
func isPreconditionFailedAPIError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	code := apiErr.ErrorCode()
	return code == "PreconditionFailed"
}

// isNoSuchKey reports whether err indicates the S3 object is absent.
// Some deployments (MinIO, older SDKs) surface this as an APIError code
// rather than the strongly typed NoSuchKey struct, so we accept both.
func isNoSuchKey(err error) bool {
	if err == nil {
		return false
	}
	var nsk *s3types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		return code == "NoSuchKey" || strings.EqualFold(code, "NotFound")
	}
	return false
}

// clientBackend is the production backend that dispatches directly to
// the S3 client and normalises precondition-failed errors to the
// canonical sentinel used by the tick logic.
type clientBackend struct{ c *s3.Client }

func (b *clientBackend) getObject(ctx context.Context, in *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
	return b.c.GetObject(ctx, in)
}

func (b *clientBackend) putObject(ctx context.Context, in *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
	out, err := b.c.PutObject(ctx, in)
	if isPreconditionFailedAPIError(err) {
		return nil, errPreconditionFailed
	}
	return out, err
}

func (b *clientBackend) deleteObject(ctx context.Context, in *s3.DeleteObjectInput) (*s3.DeleteObjectOutput, error) {
	out, err := b.c.DeleteObject(ctx, in)
	if isPreconditionFailedAPIError(err) {
		return nil, errPreconditionFailed
	}
	return out, err
}
