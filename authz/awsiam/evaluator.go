package awsiam

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/iam/types"
	"oss.nandlabs.io/golly/authz"
)

// IAMCaller is the minimal surface the evaluator uses on an AWS IAM
// client. The concrete *iam.Client from aws-sdk-go-v2 satisfies it
// directly; tests inject a fake.
type IAMCaller interface {
	SimulatePrincipalPolicy(ctx context.Context, in *iam.SimulatePrincipalPolicyInput, opts ...func(*iam.Options)) (*iam.SimulatePrincipalPolicyOutput, error)
}

// PrincipalMemberFunc extracts an AWS IAM member string (typically an
// IAM user or role ARN such as "arn:aws:iam::123456789012:role/svc")
// from an authz.Principal. Callers supply one via WithPrincipalMember
// when their Principal implementation doesn't expose an
// ID()/Member()/Arn() accessor.
type PrincipalMemberFunc func(p authz.Principal) string

// IAMResource is passed as the `resource any` argument to Evaluator.Can
// when the caller wants a live iam:SimulatePrincipalPolicy round-trip
// instead of consulting the cached allowed-actions map. It carries the
// context and the fully-qualified AWS resource ARN.
//
// This is a workaround for the fact that authz.Policy.Can does not take
// a context.Context. Callers that don't need live checks can just pass
// any value they like (including nil) as the `resource any` argument.
type IAMResource struct {
	// Ctx is the request context used for the live call. If nil,
	// context.Background() is used.
	Ctx context.Context
	// Arn is the fully-qualified AWS resource ARN the action is
	// evaluated against (e.g. "arn:aws:s3:::my-bucket/foo"). If
	// empty, the evaluator's configured resource ARN is used.
	Arn string
}

// Option customises an Evaluator at construction time.
type Option func(*Evaluator)

// WithPrincipalMember overrides the extractor used to derive an IAM
// member string (the principal ARN) from an authz.Principal. The
// default extractor tries interface{ ID() string }, then
// interface{ Arn() string }, then interface{ Member() string } via
// type-assertion and returns "" if none is present.
func WithPrincipalMember(fn PrincipalMemberFunc) Option {
	return func(e *Evaluator) {
		if fn != nil {
			e.extractor = fn
		}
	}
}

// WithExpectedMember restricts Can to grant only when the extracted
// principal-member equals `member`. Use this when the evaluator has
// been wired to a specific principal ARN and you want callers whose
// Principal resolves to a different member to be denied without a
// cache lookup or live call.
func WithExpectedMember(member string) Option {
	return func(e *Evaluator) { e.expected = member }
}

// WithInitialPermissions seeds the cache with a pre-computed
// allowed-actions map before the first Refresh runs. Useful when the
// process starts up with a known set of permissions and shouldn't have
// to wait a full refresh interval before Can can answer.
func WithInitialPermissions(perms map[string]bool) Option {
	return func(e *Evaluator) {
		if perms == nil {
			return
		}
		next := make(map[string]bool, len(perms))
		for k, v := range perms {
			if k != "" {
				next[k] = v
			}
		}
		e.perms = next
	}
}

// Evaluator implements authz.Policy by consulting an AWS IAM
// allowed-actions map cached in memory. A background goroutine re-runs
// iam:SimulatePrincipalPolicy every `refresh` interval to keep the map
// current.
//
// Can also accepts an IAMResource / *IAMResource as its `resource any`
// argument to force a live check that bypasses the cache — see
// IAMResource for details.
type Evaluator struct {
	caller       IAMCaller
	principalArn string
	resource     string
	actions      []string
	refresh      time.Duration
	extractor    PrincipalMemberFunc
	expected     string

	mu    sync.RWMutex
	perms map[string]bool // action -> allowed for the configured principalArn

	stopCh   chan struct{}
	doneCh   chan struct{}
	closedMu sync.Mutex
	closed   bool
}

// NewCachedEvaluator wires the evaluator to a real *iam.Client. It is a
// thin wrapper over NewCachedEvaluatorFromCaller for the common case.
// principalArn is the ARN whose permissions we are caching. resource is
// the target ARN the actions are simulated against. actions is the set
// of IAM API actions to pre-fetch (e.g. "s3:GetObject"). Pass 0 (or a
// negative value) for `refresh` to disable the background refresh
// goroutine — the caller then invokes Refresh manually.
func NewCachedEvaluator(client *iam.Client, principalArn, resource string, actions []string, refresh time.Duration, opts ...Option) *Evaluator {
	return NewCachedEvaluatorFromCaller(client, principalArn, resource, actions, refresh, opts...)
}

// NewCachedEvaluatorFromCaller is the general constructor: any client
// that satisfies IAMCaller can be plugged in. Tests use it to inject a
// fake caller.
func NewCachedEvaluatorFromCaller(caller IAMCaller, principalArn, resource string, actions []string, refresh time.Duration, opts ...Option) *Evaluator {
	trimmed := make([]string, 0, len(actions))
	seen := make(map[string]struct{}, len(actions))
	for _, a := range actions {
		if a == "" {
			continue
		}
		if _, dup := seen[a]; dup {
			continue
		}
		seen[a] = struct{}{}
		trimmed = append(trimmed, a)
	}
	e := &Evaluator{
		caller:       caller,
		principalArn: principalArn,
		resource:     resource,
		actions:      trimmed,
		refresh:      refresh,
		extractor:    defaultPrincipalMember,
		perms:        make(map[string]bool),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(e)
		}
	}
	if refresh > 0 {
		go e.loop()
	} else {
		close(e.doneCh)
	}
	return e
}

// defaultPrincipalMember tries a couple of common accessor shapes
// before giving up. Callers with a bespoke Principal type should inject
// an extractor via WithPrincipalMember.
func defaultPrincipalMember(p authz.Principal) string {
	if p == nil {
		return ""
	}
	if withID, ok := p.(interface{ ID() string }); ok {
		return withID.ID()
	}
	if withArn, ok := p.(interface{ Arn() string }); ok {
		return withArn.Arn()
	}
	if withMember, ok := p.(interface{ Member() string }); ok {
		return withMember.Member()
	}
	return ""
}

func (e *Evaluator) loop() {
	defer close(e.doneCh)
	t := time.NewTicker(e.refresh)
	defer t.Stop()
	for {
		select {
		case <-e.stopCh:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), e.refresh)
			_ = e.Refresh(ctx)
			cancel()
		}
	}
}

// Refresh re-runs iam:SimulatePrincipalPolicy for the configured
// principal, resource, and action set, and replaces the cached
// allowed-actions map. Safe for concurrent use with Can. Returns
// whatever error the underlying IAM call returned; the cache is left
// untouched on error.
func (e *Evaluator) Refresh(ctx context.Context) error {
	if e == nil || e.caller == nil {
		return errors.New("awsiam: evaluator has no caller")
	}
	if len(e.actions) == 0 {
		return nil
	}
	in := &iam.SimulatePrincipalPolicyInput{
		PolicySourceArn: aws.String(e.principalArn),
		ActionNames:     append([]string(nil), e.actions...),
	}
	if e.resource != "" {
		in.ResourceArns = []string{e.resource}
	}
	resp, err := e.caller.SimulatePrincipalPolicy(ctx, in)
	if err != nil {
		return fmt.Errorf("awsiam: SimulatePrincipalPolicy on %q: %w", e.resource, err)
	}
	next := make(map[string]bool, len(e.actions))
	for _, a := range e.actions {
		next[a] = false
	}
	for _, r := range resp.EvaluationResults {
		if r.EvalActionName == nil {
			continue
		}
		next[*r.EvalActionName] = r.EvalDecision == types.PolicyEvaluationDecisionTypeAllowed
	}
	e.mu.Lock()
	e.perms = next
	e.mu.Unlock()
	return nil
}

// Can satisfies authz.Policy.
//
// If `resource` is an IAMResource or *IAMResource, Can bypasses the
// cache and makes a live iam:SimulatePrincipalPolicy call using the
// embedded context and ARN. Otherwise it consults the cached
// allowed-actions map. Actions not present in the cache resolve to
// false — the caller is expected to seed them via the actions slice
// passed to NewCachedEvaluator or via WithInitialPermissions.
func (e *Evaluator) Can(p authz.Principal, action string, resource any) bool {
	if e == nil || action == "" {
		return false
	}
	if !e.principalOK(p) {
		return false
	}
	if live, arn, ctx, ok := extractIAMResource(resource); ok {
		_ = live
		// Empty ARN on the envelope means "no target resource to
		// simulate against" — fall through to the cache rather than
		// dispatch a malformed live call.
		if arn != "" {
			return e.liveCheck(ctx, arn, action)
		}
	}
	e.mu.RLock()
	allowed := e.perms[action]
	e.mu.RUnlock()
	return allowed
}

// principalOK enforces WithExpectedMember: when set, the extracted
// principal-member must equal the expected ARN or the call is denied
// before any cache lookup or live IAM call. When WithExpectedMember is
// unset, no principal-gating happens — the cache reflects the
// configured principalArn, not the calling identity.
func (e *Evaluator) principalOK(p authz.Principal) bool {
	if e.expected == "" {
		return true
	}
	return e.extractor(p) == e.expected
}

func (e *Evaluator) liveCheck(ctx context.Context, arn, action string) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	in := &iam.SimulatePrincipalPolicyInput{
		PolicySourceArn: aws.String(e.principalArn),
		ActionNames:     []string{action},
		ResourceArns:    []string{arn},
	}
	resp, err := e.caller.SimulatePrincipalPolicy(ctx, in)
	if err != nil {
		return false
	}
	for _, r := range resp.EvaluationResults {
		if r.EvalActionName == nil || *r.EvalActionName != action {
			continue
		}
		if r.EvalDecision == types.PolicyEvaluationDecisionTypeAllowed {
			return true
		}
	}
	return false
}

// extractIAMResource unwraps IAMResource / *IAMResource from the any.
// Returns (present, arn, ctx, ok).
func extractIAMResource(resource any) (IAMResource, string, context.Context, bool) {
	switch r := resource.(type) {
	case IAMResource:
		return r, r.Arn, r.Ctx, true
	case *IAMResource:
		if r == nil {
			return IAMResource{}, "", nil, false
		}
		return *r, r.Arn, r.Ctx, true
	}
	return IAMResource{}, "", nil, false
}

// Close stops the background refresh goroutine. Safe to call multiple
// times. Returns nil — the error return exists for symmetry with
// io.Closer.
func (e *Evaluator) Close() error {
	if e == nil {
		return nil
	}
	e.closedMu.Lock()
	if e.closed {
		e.closedMu.Unlock()
		return nil
	}
	e.closed = true
	close(e.stopCh)
	e.closedMu.Unlock()
	<-e.doneCh
	return nil
}
