package awsiam

import (
	"context"
	"testing"
)

func TestCan_IAMResource_LiveCall_Granted(t *testing.T) {
	t.Parallel()
	fc := newFakeCaller("s3:GetObject")
	// No actions configured — the live path must not touch the cache.
	e := NewCachedEvaluatorFromCaller(fc, testPrincipalArn, "unused", nil, 0)
	defer func() { _ = e.Close() }()

	p := &principalWithID{id: testPrincipalArn}
	res := IAMResource{
		Ctx: context.Background(),
		Arn: "arn:aws:s3:::photos/foo.jpg",
	}
	if !e.Can(p, "s3:GetObject", res) {
		t.Fatal("live check granted action: want true")
	}
	if got := fc.callCount(); got != 1 {
		t.Fatalf("expected exactly 1 live call, got %d", got)
	}
	// The request must carry the IAMResource.Arn, not the evaluator's
	// configured resource.
	fc.mu.Lock()
	got := fc.lastReq
	fc.mu.Unlock()
	if got == nil {
		t.Fatal("no request recorded")
	}
	if len(got.ResourceArns) != 1 || got.ResourceArns[0] != "arn:aws:s3:::photos/foo.jpg" {
		t.Fatalf("ResourceArns: want [%q], got %v", "arn:aws:s3:::photos/foo.jpg", got.ResourceArns)
	}
	if len(got.ActionNames) != 1 || got.ActionNames[0] != "s3:GetObject" {
		t.Fatalf("ActionNames: want [s3:GetObject], got %v", got.ActionNames)
	}
}

func TestCan_IAMResource_LiveCall_Denied(t *testing.T) {
	t.Parallel()
	fc := newFakeCaller() // nothing granted
	e := NewCachedEvaluatorFromCaller(fc, testPrincipalArn, testBucketArn, nil, 0)
	defer func() { _ = e.Close() }()

	p := &principalWithID{id: testPrincipalArn}
	res := &IAMResource{
		Ctx: context.Background(),
		Arn: "arn:aws:s3:::photos/foo.jpg",
	}
	if e.Can(p, "s3:DeleteObject", res) {
		t.Fatal("live check ungranted action: want false")
	}
}

func TestCan_IAMResource_NilCtx_UsesBackground(t *testing.T) {
	t.Parallel()
	fc := newFakeCaller("s3:GetObject")
	e := NewCachedEvaluatorFromCaller(fc, testPrincipalArn, testBucketArn, nil, 0)
	defer func() { _ = e.Close() }()

	p := &principalWithID{id: testPrincipalArn}
	// Ctx is nil — must not panic; evaluator falls back to
	// context.Background().
	res := IAMResource{Arn: "arn:aws:s3:::photos/foo.jpg"}
	if !e.Can(p, "s3:GetObject", res) {
		t.Fatal("nil ctx live path: want true")
	}
	if got := fc.callCount(); got != 1 {
		t.Fatalf("expected 1 live call, got %d", got)
	}
}

func TestCan_IAMResource_EmptyArn_FallsThroughToCache(t *testing.T) {
	t.Parallel()
	// The cache has s3:GetObject seeded; the live path must NOT be
	// invoked when IAMResource.Arn is empty — instead Can consults the
	// cache and returns its answer.
	fc := newFakeCaller() // nothing granted live
	e := NewCachedEvaluatorFromCaller(fc, testPrincipalArn, testBucketArn,
		[]string{"s3:GetObject"}, 0,
		WithInitialPermissions(map[string]bool{"s3:GetObject": true}))
	defer func() { _ = e.Close() }()

	p := &principalWithID{id: testPrincipalArn}
	res := IAMResource{Ctx: context.Background()} // no Arn
	if !e.Can(p, "s3:GetObject", res) {
		t.Fatal("empty Arn: expected cache answer (true)")
	}
	if got := fc.callCount(); got != 0 {
		t.Fatalf("expected zero live calls when Arn is empty, got %d", got)
	}
	// And an action absent from the cache is denied without a live call.
	if e.Can(p, "s3:PutObject", res) {
		t.Fatal("empty Arn, uncached action: want false")
	}
	if got := fc.callCount(); got != 0 {
		t.Fatalf("expected zero live calls, got %d", got)
	}
}

func TestCan_IAMResource_ExpectedMemberMismatch_ShortCircuits(t *testing.T) {
	t.Parallel()
	fc := newFakeCaller("s3:GetObject")
	e := NewCachedEvaluatorFromCaller(fc, testPrincipalArn, testBucketArn, nil, 0,
		WithExpectedMember(testPrincipalArn))
	defer func() { _ = e.Close() }()

	stranger := &principalWithID{id: "arn:aws:iam::123456789012:role/other"}
	res := IAMResource{Ctx: context.Background(), Arn: "arn:aws:s3:::photos/foo.jpg"}
	if e.Can(stranger, "s3:GetObject", res) {
		t.Fatal("expected-member mismatch on live path: want false")
	}
	if got := fc.callCount(); got != 0 {
		t.Fatalf("expected zero live calls for mismatched principal, got %d", got)
	}
}

func TestCan_IAMResource_PointerNil_TreatedAsPlainResource(t *testing.T) {
	t.Parallel()
	// A nil *IAMResource must not be dereferenced. Since it's not a
	// recognisable IAMResource envelope, Can falls through to the cache.
	fc := newFakeCaller()
	e := NewCachedEvaluatorFromCaller(fc, testPrincipalArn, testBucketArn,
		[]string{"s3:GetObject"}, 0,
		WithInitialPermissions(map[string]bool{"s3:GetObject": true}))
	defer func() { _ = e.Close() }()

	p := &principalWithID{id: testPrincipalArn}
	var nilRes *IAMResource
	if !e.Can(p, "s3:GetObject", nilRes) {
		t.Fatal("nil *IAMResource: expected cache answer (true)")
	}
	if got := fc.callCount(); got != 0 {
		t.Fatalf("expected zero live calls, got %d", got)
	}
}
