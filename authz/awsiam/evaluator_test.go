package awsiam

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/iam/types"
	"oss.nandlabs.io/golly/authz"
)

// principalWithID is a test Principal that also exposes ID() so the
// default extractor picks it up via type-assertion.
type principalWithID struct {
	id    string
	roles []string
	caps  []string
}

func (p *principalWithID) ID() string             { return p.id }
func (p *principalWithID) Roles() []string        { return p.roles }
func (p *principalWithID) Capabilities() []string { return p.caps }

// fakeCaller records every SimulatePrincipalPolicy call and returns an
// evaluation result for each action driven by the `granted` map. Tests
// mutate granted / err between refreshes.
type fakeCaller struct {
	mu       sync.Mutex
	granted  map[string]bool // action -> allowed on this resource
	calls    int
	lastReq  *iam.SimulatePrincipalPolicyInput
	err      error
	callHook func()
}

func newFakeCaller(granted ...string) *fakeCaller {
	m := make(map[string]bool, len(granted))
	for _, g := range granted {
		m[g] = true
	}
	return &fakeCaller{granted: m}
}

func (f *fakeCaller) SimulatePrincipalPolicy(_ context.Context, in *iam.SimulatePrincipalPolicyInput, _ ...func(*iam.Options)) (*iam.SimulatePrincipalPolicyOutput, error) {
	f.mu.Lock()
	f.calls++
	f.lastReq = in
	hook := f.callHook
	err := f.err
	grantedCopy := make(map[string]bool, len(f.granted))
	for k, v := range f.granted {
		grantedCopy[k] = v
	}
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if err != nil {
		return nil, err
	}
	out := &iam.SimulatePrincipalPolicyOutput{}
	for _, a := range in.ActionNames {
		action := a
		decision := types.PolicyEvaluationDecisionTypeImplicitDeny
		if grantedCopy[action] {
			decision = types.PolicyEvaluationDecisionTypeAllowed
		}
		out.EvaluationResults = append(out.EvaluationResults, types.EvaluationResult{
			EvalActionName: aws.String(action),
			EvalDecision:   decision,
		})
	}
	return out, nil
}

func (f *fakeCaller) setGranted(perms ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.granted = map[string]bool{}
	for _, p := range perms {
		f.granted[p] = true
	}
}

func (f *fakeCaller) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeCaller) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

const (
	testPrincipalArn = "arn:aws:iam::123456789012:role/svc"
	testBucketArn    = "arn:aws:s3:::my-bucket"
)

func TestCachedEvaluator_HitsCacheBetweenRefresh(t *testing.T) {
	t.Parallel()
	fc := newFakeCaller("s3:GetObject")
	// refresh=0 disables the background loop; we drive Refresh manually.
	e := NewCachedEvaluatorFromCaller(fc, testPrincipalArn, testBucketArn,
		[]string{"s3:GetObject"}, 0)
	defer func() { _ = e.Close() }()

	if err := e.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := fc.callCount(); got != 1 {
		t.Fatalf("expected 1 call after Refresh, got %d", got)
	}

	p := &principalWithID{id: testPrincipalArn}
	for i := 0; i < 5; i++ {
		if !e.Can(p, "s3:GetObject", nil) {
			t.Fatalf("Can #%d: expected true from cache", i)
		}
	}
	if got := fc.callCount(); got != 1 {
		t.Fatalf("expected only cache hits, got %d additional calls", got-1)
	}
}

func TestCachedEvaluator_RefreshUpdatesCache(t *testing.T) {
	t.Parallel()
	fc := newFakeCaller("s3:GetObject")
	e := NewCachedEvaluatorFromCaller(fc, testPrincipalArn, testBucketArn,
		[]string{"s3:GetObject"}, 0)
	defer func() { _ = e.Close() }()

	if err := e.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}

	p := &principalWithID{id: testPrincipalArn}
	if !e.Can(p, "s3:GetObject", nil) {
		t.Fatal("Can before revoke: want true")
	}

	// IAM revokes the permission; next Refresh should reflect that.
	fc.setGranted()
	if err := e.Refresh(context.Background()); err != nil {
		t.Fatalf("second Refresh: %v", err)
	}
	if e.Can(p, "s3:GetObject", nil) {
		t.Fatal("Can after revoke: want false")
	}

	// Re-grant; cache flips back after another Refresh.
	fc.setGranted("s3:GetObject")
	if err := e.Refresh(context.Background()); err != nil {
		t.Fatalf("third Refresh: %v", err)
	}
	if !e.Can(p, "s3:GetObject", nil) {
		t.Fatal("Can after re-grant: want true")
	}
}

func TestCachedEvaluator_MissingPermission_ReturnsFalse(t *testing.T) {
	t.Parallel()
	fc := newFakeCaller("s3:GetObject") // ListBucket is NOT granted
	e := NewCachedEvaluatorFromCaller(fc, testPrincipalArn, testBucketArn,
		[]string{"s3:GetObject", "s3:ListBucket"}, 0)
	defer func() { _ = e.Close() }()

	if err := e.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	p := &principalWithID{id: testPrincipalArn}
	if !e.Can(p, "s3:GetObject", nil) {
		t.Fatal("granted action: want true")
	}
	if e.Can(p, "s3:ListBucket", nil) {
		t.Fatal("ungranted action: want false")
	}
	if e.Can(p, "s3:DeleteObject", nil) {
		t.Fatal("action not in configured set: want false (uncached => deny)")
	}
}

func TestCachedEvaluator_UnknownPrincipal_ReturnsFalse(t *testing.T) {
	t.Parallel()
	fc := newFakeCaller("s3:GetObject")
	// Pin the evaluator to a specific principal ARN so mismatches are denied.
	e := NewCachedEvaluatorFromCaller(fc, testPrincipalArn, testBucketArn,
		[]string{"s3:GetObject"}, 0,
		WithExpectedMember(testPrincipalArn))
	defer func() { _ = e.Close() }()
	if err := e.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// A principal whose ID doesn't match the expected ARN is denied.
	stranger := &principalWithID{id: "arn:aws:iam::123456789012:role/other"}
	if e.Can(stranger, "s3:GetObject", nil) {
		t.Fatal("wrong-member principal: want false")
	}
	// authz.BasicPrincipal has no ID accessor so extractor returns ""
	// which cannot equal the expected ARN — also denied.
	unknown := &authz.BasicPrincipal{RoleList: []string{"viewer"}}
	if e.Can(unknown, "s3:GetObject", nil) {
		t.Fatal("no-ID principal against expected member: want false")
	}
	// The matching principal is granted.
	svc := &principalWithID{id: testPrincipalArn}
	if !e.Can(svc, "s3:GetObject", nil) {
		t.Fatal("expected member: want true")
	}
}

func TestCachedEvaluator_BackgroundLoop_RefreshesOnInterval(t *testing.T) {
	t.Parallel()
	fc := newFakeCaller("s3:GetObject")
	e := NewCachedEvaluatorFromCaller(fc, testPrincipalArn, testBucketArn,
		[]string{"s3:GetObject"}, 20*time.Millisecond)
	defer func() { _ = e.Close() }()

	// Wait for at least two background refreshes so we know the ticker
	// is actually looping and not just firing once.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fc.callCount() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := fc.callCount(); got < 2 {
		t.Fatalf("background loop should refresh at least twice: got %d", got)
	}
	p := &principalWithID{id: testPrincipalArn}
	if !e.Can(p, "s3:GetObject", nil) {
		t.Fatal("after background refresh: want true")
	}
}

func TestCachedEvaluator_RefreshError_PreservesCache(t *testing.T) {
	t.Parallel()
	fc := newFakeCaller("s3:GetObject")
	e := NewCachedEvaluatorFromCaller(fc, testPrincipalArn, testBucketArn,
		[]string{"s3:GetObject"}, 0)
	defer func() { _ = e.Close() }()

	if err := e.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	p := &principalWithID{id: testPrincipalArn}
	if !e.Can(p, "s3:GetObject", nil) {
		t.Fatal("pre-error: want true")
	}
	// IAM now fails; the cache from the previous successful Refresh
	// must not be clobbered.
	fc.setErr(errors.New("boom"))
	if err := e.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh with err: want error")
	}
	if !e.Can(p, "s3:GetObject", nil) {
		t.Fatal("post-error: cache should still say true")
	}
}

func TestCachedEvaluator_Close_Idempotent(t *testing.T) {
	t.Parallel()
	fc := newFakeCaller()
	e := NewCachedEvaluatorFromCaller(fc, testPrincipalArn, testBucketArn,
		[]string{"s3:GetObject"}, 20*time.Millisecond)
	if err := e.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestCachedEvaluator_InitialPermissions_SeedsCache(t *testing.T) {
	t.Parallel()
	// No Refresh has been driven and the fake caller is not consulted,
	// but Can still resolves from the seeded cache.
	fc := newFakeCaller()
	e := NewCachedEvaluatorFromCaller(fc, testPrincipalArn, testBucketArn,
		[]string{"s3:GetObject", "s3:PutObject"}, 0,
		WithInitialPermissions(map[string]bool{
			"s3:GetObject": true,
			"s3:PutObject": false,
		}))
	defer func() { _ = e.Close() }()

	p := &principalWithID{id: testPrincipalArn}
	if !e.Can(p, "s3:GetObject", nil) {
		t.Fatal("seeded allow: want true")
	}
	if e.Can(p, "s3:PutObject", nil) {
		t.Fatal("seeded deny: want false")
	}
	if got := fc.callCount(); got != 0 {
		t.Fatalf("expected no IAM calls, got %d", got)
	}
}

func TestCachedEvaluator_Refresh_RequestShape(t *testing.T) {
	t.Parallel()
	fc := newFakeCaller("s3:GetObject")
	actions := []string{"s3:GetObject", "s3:ListBucket"}
	e := NewCachedEvaluatorFromCaller(fc, testPrincipalArn, testBucketArn, actions, 0)
	defer func() { _ = e.Close() }()

	if err := e.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	fc.mu.Lock()
	got := fc.lastReq
	fc.mu.Unlock()
	if got == nil {
		t.Fatal("no request recorded")
	}
	if got.PolicySourceArn == nil || *got.PolicySourceArn != testPrincipalArn {
		t.Fatalf("PolicySourceArn: got %v, want %q", got.PolicySourceArn, testPrincipalArn)
	}
	if len(got.ResourceArns) != 1 || got.ResourceArns[0] != testBucketArn {
		t.Fatalf("ResourceArns: got %v, want [%q]", got.ResourceArns, testBucketArn)
	}
	if len(got.ActionNames) != 2 {
		t.Fatalf("ActionNames: got %v", got.ActionNames)
	}
}
