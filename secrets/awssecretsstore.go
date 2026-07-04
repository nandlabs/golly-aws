package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	secrets "oss.nandlabs.io/golly/secrets"
)

const (
	AWSSecretsManagerProvider = "aws-secrets-manager"
)

// secretsAPI is the subset of the AWS Secrets Manager client that
// AWSSecretsStore uses. Extracted so tests can substitute a mock; production
// paths always receive the concrete *secretsmanager.Client.
type secretsAPI interface {
	GetSecretValue(ctx context.Context, in *secretsmanager.GetSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
	DescribeSecret(ctx context.Context, in *secretsmanager.DescribeSecretInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.DescribeSecretOutput, error)
	CreateSecret(ctx context.Context, in *secretsmanager.CreateSecretInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.CreateSecretOutput, error)
	PutSecretValue(ctx context.Context, in *secretsmanager.PutSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.PutSecretValueOutput, error)
}

// AWSSecretsStore implements the golly secrets.Store interface using AWS
// Secrets Manager.
//
// It intentionally implements only the methods on secrets.Store (Get, Write,
// Provider). No public Delete is exposed: the upstream Store interface has
// no Delete, so a public Delete on this type would bypass secrets.Namespaced
// + WithAuthorizer and let a caller destroy tenant secrets without a policy
// check. Delete support should be added upstream first (as an optional
// Deleter interface tied to OpDelete) before it re-appears here.
type AWSSecretsStore struct {
	api        secretsAPI
	client     *secretsmanager.Client
	region     string
	tagFilter  map[string]string
	tenantTags map[string]string
	mutex      sync.RWMutex
	cache      map[string]*secrets.Credential
	cacheTTL   time.Duration
	lastSync   map[string]time.Time
}

// AWSSecretsStoreConfig holds configuration for creating an AWSSecretsStore
type AWSSecretsStoreConfig struct {
	Region    string            // AWS region
	TagFilter map[string]string // Tags to filter secrets on List/lookup
	CacheTTL  time.Duration     // Cache TTL (0 = no caching)
}

// Option configures an AWSSecretsStore at construction time.
type Option func(*AWSSecretsStore)

// WithTenantTags supplies a fixed set of tags that Write applies to every
// newly-created secret. Prefer this over reusing TagFilter for writes:
// TagFilter is a global lookup filter and applying it to every write leaks
// the same tags onto secrets belonging to different tenants. Callers that
// namespace a store per tenant should pass their tenant's tags here.
//
//	tenantStore, _ := NewAWSSecretsStore(ctx, cfg,
//	    WithTenantTags(map[string]string{"tenant": tenantID}),
//	)
//	scoped := secrets.Namespace(tenantStore, "tenant/"+tenantID,
//	    secrets.WithAuthorizer(policy))
//
// When set, tenant tags replace (not merge with) the TagFilter values on
// CreateSecret calls; a nil or empty map falls back to the config TagFilter
// for backwards compatibility.
func WithTenantTags(tags map[string]string) Option {
	return func(as *AWSSecretsStore) {
		if len(tags) == 0 {
			return
		}
		copied := make(map[string]string, len(tags))
		for k, v := range tags {
			copied[k] = v
		}
		as.tenantTags = copied
	}
}

// NewAWSSecretsStore creates a new AWS Secrets Manager-backed store. Extra
// options (e.g. WithTenantTags) are applied after cfg is consumed.
func NewAWSSecretsStore(ctx context.Context, cfg *AWSSecretsStoreConfig, opts ...Option) (*AWSSecretsStore, error) {
	if cfg == nil {
		cfg = &AWSSecretsStoreConfig{
			Region: "us-east-1",
		}
	}

	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}

	// Load AWS config
	awsConfig, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := secretsmanager.NewFromConfig(awsConfig)

	store := &AWSSecretsStore{
		api:       client,
		client:    client,
		region:    cfg.Region,
		tagFilter: cfg.TagFilter,
		cache:     make(map[string]*secrets.Credential),
		cacheTTL:  cfg.CacheTTL,
		lastSync:  make(map[string]time.Time),
	}

	for _, o := range opts {
		o(store)
	}

	return store, nil
}

// Get retrieves a credential from AWS Secrets Manager
func (as *AWSSecretsStore) Get(key string, ctx context.Context) (*secrets.Credential, error) {
	as.mutex.RLock()
	defer as.mutex.RUnlock()

	// Check cache
	if cached, ok := as.cache[key]; ok {
		if as.cacheTTL == 0 || time.Since(as.lastSync[key]) < as.cacheTTL {
			return cached, nil
		}
	}

	input := &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(key),
	}

	result, err := as.api.GetSecretValue(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret from AWS Secrets Manager: %w", err)
	}

	cred := &secrets.Credential{
		LastUpdated: time.Now(),
		MetaData:    make(map[string]interface{}),
	}

	// Parse secret value
	if result.SecretString != nil {
		// Try to parse as JSON credential
		var credData map[string]interface{}
		if err := json.Unmarshal([]byte(*result.SecretString), &credData); err == nil {
			// Extract credential fields
			if value, ok := credData["value"]; ok {
				cred.Value = []byte(fmt.Sprintf("%v", value))
			}
			if version, ok := credData["version"].(string); ok {
				cred.Version = version
			}
			if metadata, ok := credData["metadata"].(map[string]interface{}); ok {
				cred.MetaData = metadata
			}
		} else {
			// Store raw secret string
			cred.Value = []byte(*result.SecretString)
		}
	} else if result.SecretBinary != nil {
		cred.Value = result.SecretBinary
	}

	// Add AWS metadata
	if result.VersionId != nil {
		cred.MetaData["aws_version_id"] = *result.VersionId
	}

	if result.ARN != nil {
		cred.MetaData["aws_arn"] = *result.ARN
	}

	// Update cache
	as.cache[key] = cred
	as.lastSync[key] = time.Now()

	return cred, nil
}

// Write stores a credential in AWS Secrets Manager
func (as *AWSSecretsStore) Write(key string, credential *secrets.Credential, ctx context.Context) error {
	as.mutex.Lock()
	defer as.mutex.Unlock()

	// Prepare secret data
	secretData := map[string]interface{}{
		"value":        string(credential.Value),
		"version":      credential.Version,
		"last_updated": credential.LastUpdated.Unix(),
	}

	if credential.MetaData != nil {
		secretData["metadata"] = credential.MetaData
	}

	secretString, err := json.Marshal(secretData)
	if err != nil {
		return fmt.Errorf("failed to marshal credential: %w", err)
	}

	// Check if secret exists
	getInput := &secretsmanager.DescribeSecretInput{
		SecretId: aws.String(key),
	}

	_, err = as.api.DescribeSecret(ctx, getInput)
	if err != nil {
		// Secret doesn't exist, create it
		createInput := &secretsmanager.CreateSecretInput{
			Name:         aws.String(key),
			SecretString: aws.String(string(secretString)),
		}

		// Prefer per-tenant tags supplied via WithTenantTags. Fall back to
		// the legacy TagFilter only when no tenant tags were configured so
		// existing callers keep working. TagFilter is a lookup filter, not
		// a per-write tag set, and applying it to every write leaks the
		// same tags across tenants — new callers should use WithTenantTags.
		writeTags := as.tenantTags
		if writeTags == nil {
			writeTags = as.tagFilter
		}
		if len(writeTags) > 0 {
			tags := make([]types.Tag, 0, len(writeTags))
			for k, v := range writeTags {
				k, v := k, v // Copy for pointer
				tags = append(tags, types.Tag{
					Key:   aws.String(k),
					Value: aws.String(v),
				})
			}
			createInput.Tags = tags
		}

		_, err := as.api.CreateSecret(ctx, createInput)
		if err != nil {
			return fmt.Errorf("failed to create secret in AWS Secrets Manager: %w", err)
		}
	} else {
		// Secret exists, update it
		updateInput := &secretsmanager.PutSecretValueInput{
			SecretId:     aws.String(key),
			SecretString: aws.String(string(secretString)),
		}

		_, err := as.api.PutSecretValue(ctx, updateInput)
		if err != nil {
			return fmt.Errorf("failed to update secret in AWS Secrets Manager: %w", err)
		}
	}

	// Update cache
	as.cache[key] = credential
	as.lastSync[key] = time.Now()

	return nil
}

// List lists all credentials
func (as *AWSSecretsStore) List(ctx context.Context) ([]string, error) {
	as.mutex.RLock()
	defer as.mutex.RUnlock()

	paginator := secretsmanager.NewListSecretsPaginator(as.client, &secretsmanager.ListSecretsInput{})

	var results []string
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list secrets: %w", err)
		}

		for _, secret := range page.SecretList {
			if secret.Name != nil {
				results = append(results, *secret.Name)
			}
		}
	}

	return results, nil
}

// Provider returns the provider name
func (as *AWSSecretsStore) Provider() string {
	return AWSSecretsManagerProvider
}

// ClearCache clears the in-memory cache
func (as *AWSSecretsStore) ClearCache() {
	as.mutex.Lock()
	defer as.mutex.Unlock()

	as.cache = make(map[string]*secrets.Credential)
	as.lastSync = make(map[string]time.Time)
}

// GetClient returns the underlying AWS Secrets Manager client for advanced operations
func (as *AWSSecretsStore) GetClient() *secretsmanager.Client {
	return as.client
}
