package secrets

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	secrets "oss.nandlabs.io/golly/secrets"
)

// mockSecretsAPI is a minimal in-memory stand-in for the AWS Secrets Manager
// client. Every call increments a counter and (for CreateSecret) captures the
// most recent input so tests can assert on it.
type mockSecretsAPI struct {
	mu sync.Mutex

	getCalls      int
	describeCalls int
	createCalls   int
	putCalls      int

	// describeErr, when non-nil, is returned from DescribeSecret; when nil,
	// DescribeSecret returns an empty output (i.e. "secret exists"). Tests
	// force the Create path by supplying a non-nil error.
	describeErr error

	lastCreate *secretsmanager.CreateSecretInput
}

func (m *mockSecretsAPI) GetSecretValue(ctx context.Context, in *secretsmanager.GetSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getCalls++
	return &secretsmanager.GetSecretValueOutput{
		SecretString: aws.String(`{"value":"v","version":"1"}`),
	}, nil
}

func (m *mockSecretsAPI) DescribeSecret(ctx context.Context, in *secretsmanager.DescribeSecretInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.DescribeSecretOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.describeCalls++
	if m.describeErr != nil {
		return nil, m.describeErr
	}
	return &secretsmanager.DescribeSecretOutput{}, nil
}

func (m *mockSecretsAPI) CreateSecret(ctx context.Context, in *secretsmanager.CreateSecretInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.CreateSecretOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createCalls++
	m.lastCreate = in
	return &secretsmanager.CreateSecretOutput{ARN: aws.String("arn:aws:secretsmanager:us-east-1:0:secret:x")}, nil
}

func (m *mockSecretsAPI) PutSecretValue(ctx context.Context, in *secretsmanager.PutSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.PutSecretValueOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.putCalls++
	return &secretsmanager.PutSecretValueOutput{}, nil
}

// newStoreWithAPI builds an AWSSecretsStore with an injected mock API. It
// skips the AWS config loader entirely, so tests never need credentials.
func newStoreWithAPI(api secretsAPI, opts ...Option) *AWSSecretsStore {
	s := &AWSSecretsStore{
		api:      api,
		region:   "us-east-1",
		cache:    make(map[string]*secrets.Credential),
		lastSync: make(map[string]time.Time),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// TestNamespaced_AuthorizerVetoesGet_OnAWSStore proves that wrapping the AWS
// store in secrets.Namespaced with a deny-all authorizer blocks Get before
// any AWS API call is made — the gap this change closes.
func TestNamespaced_AuthorizerVetoesGet_OnAWSStore(t *testing.T) {
	mock := &mockSecretsAPI{}
	inner := newStoreWithAPI(mock)

	deny := func(ctx context.Context, op secrets.Op, key string) error {
		return fmt.Errorf("%w: get blocked for %s", secrets.ErrForbidden, key)
	}
	scoped := secrets.Namespace(inner, "tenant/acme", secrets.WithAuthorizer(deny))

	_, err := scoped.Get("db-password", context.Background())
	if err == nil {
		t.Fatal("Get: expected error, got nil")
	}
	if !errors.Is(err, secrets.ErrForbidden) {
		t.Fatalf("Get: expected ErrForbidden, got %v", err)
	}
	if mock.getCalls != 0 {
		t.Errorf("Get: expected 0 AWS calls, got %d", mock.getCalls)
	}
}

// TestNamespaced_AuthorizerVetoesWrite_OnAWSStore is the Write-path twin of
// the Get test above.
func TestNamespaced_AuthorizerVetoesWrite_OnAWSStore(t *testing.T) {
	mock := &mockSecretsAPI{}
	inner := newStoreWithAPI(mock)

	deny := func(ctx context.Context, op secrets.Op, key string) error {
		return fmt.Errorf("%w: write blocked for %s", secrets.ErrForbidden, key)
	}
	scoped := secrets.Namespace(inner, "tenant/acme", secrets.WithAuthorizer(deny))

	err := scoped.Write("db-password", &secrets.Credential{Value: []byte("s3cr3t")}, context.Background())
	if err == nil {
		t.Fatal("Write: expected error, got nil")
	}
	if !errors.Is(err, secrets.ErrForbidden) {
		t.Fatalf("Write: expected ErrForbidden, got %v", err)
	}
	if mock.describeCalls != 0 || mock.createCalls != 0 || mock.putCalls != 0 {
		t.Errorf("Write: expected 0 AWS calls, got describe=%d create=%d put=%d",
			mock.describeCalls, mock.createCalls, mock.putCalls)
	}
}

// TestNamespaced_AuthorizerPermits_ForwardsToAWS confirms the authorizer
// chain doesn't drop calls when the policy allows them: Get with a permit
// authorizer must reach the mock.
func TestNamespaced_AuthorizerPermits_ForwardsToAWS(t *testing.T) {
	mock := &mockSecretsAPI{}
	inner := newStoreWithAPI(mock)

	permit := func(ctx context.Context, op secrets.Op, key string) error { return nil }
	scoped := secrets.Namespace(inner, "tenant/acme", secrets.WithAuthorizer(permit))

	if _, err := scoped.Get("db-password", context.Background()); err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if mock.getCalls != 1 {
		t.Errorf("Get: expected 1 AWS call, got %d", mock.getCalls)
	}
}

// TestWrite_UsesTenantTags asserts the WithTenantTags option puts tenant tags
// (and only tenant tags) on the CreateSecretInput — the global TagFilter must
// not leak onto a per-tenant write.
func TestWrite_UsesTenantTags(t *testing.T) {
	mock := &mockSecretsAPI{
		// Force the create path: DescribeSecret returns an error → "doesn't exist".
		describeErr: errors.New("ResourceNotFoundException"),
	}
	inner := newStoreWithAPI(mock, WithTenantTags(map[string]string{"tenant": "acme"}))
	// Populate the *legacy* TagFilter after construction so we can prove it
	// is bypassed when tenant tags are present.
	inner.tagFilter = map[string]string{"env": "global", "app": "golly"}

	err := inner.Write("api-key", &secrets.Credential{
		Value:       []byte("k"),
		Version:     "1",
		LastUpdated: time.Now(),
	}, context.Background())
	if err != nil {
		t.Fatalf("Write: unexpected error: %v", err)
	}
	if mock.createCalls != 1 || mock.lastCreate == nil {
		t.Fatalf("Write: expected 1 CreateSecret call, got %d (input=%v)", mock.createCalls, mock.lastCreate)
	}

	got := tagsAsMap(mock.lastCreate.Tags)
	want := map[string]string{"tenant": "acme"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CreateSecret.Tags = %v, want %v (global TagFilter must not leak)", got, want)
	}
}

// TestWrite_FallsBackToTagFilter_WhenNoTenantTags preserves the legacy
// behavior for callers who haven't adopted WithTenantTags yet.
func TestWrite_FallsBackToTagFilter_WhenNoTenantTags(t *testing.T) {
	mock := &mockSecretsAPI{describeErr: errors.New("ResourceNotFoundException")}
	inner := newStoreWithAPI(mock)
	inner.tagFilter = map[string]string{"env": "global"}

	if err := inner.Write("k", &secrets.Credential{Value: []byte("v")}, context.Background()); err != nil {
		t.Fatalf("Write: unexpected error: %v", err)
	}
	got := tagsAsMap(mock.lastCreate.Tags)
	if got["env"] != "global" || len(got) != 1 {
		t.Errorf("CreateSecret.Tags = %v, want fallback {env:global}", got)
	}
}

func tagsAsMap(tags []types.Tag) map[string]string {
	out := make(map[string]string, len(tags))
	for _, t := range tags {
		if t.Key == nil || t.Value == nil {
			continue
		}
		out[*t.Key] = *t.Value
	}
	return out
}
