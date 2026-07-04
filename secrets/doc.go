// Package secrets provides AWS Secrets Manager integration for Golly credential management.
//
// This package implements the Store interface from oss.nandlabs.io/golly/secrets
// using AWS Secrets Manager as the backend storage for credentials.
//
// # Features
//
// - Full integration with Golly credential types and metadata
// - Automatic secret creation if not exists
// - Tag-based filtering and organization
// - In-memory caching with configurable TTL
// - Thread-safe concurrent operations
// - JSON-based credential storage
// - Support for binary and string secrets
//
// # Basic Usage
//
// Create a new store:
//
//	store, err := NewAWSSecretsStore(ctx, &AWSSecretsStoreConfig{
//	    Region: "us-east-1",
//	})
//
// Write a credential:
//
//	cred := &secrets.Credential{
//	    Value:       []byte("secret-value"),
//	    LastUpdated: time.Now(),
//	    Version:     "1.0",
//	}
//	err := store.Write("my-secret", cred, ctx)
//
// Read a credential:
//
//	cred, err := store.Get("my-secret", ctx)
//
// List all credentials:
//
//	keys, err := store.List(ctx)
//
// # Delete
//
// AWSSecretsStore intentionally does not expose a Delete method. The upstream
// secrets.Store interface has no Delete, so a public Delete here would bypass
// secrets.Namespaced + WithAuthorizer and let callers destroy tenant secrets
// without a policy check. Once upstream lands a Deleter optional interface
// (with an OpDelete authorization op) this store will implement it.
//
// # Multi-tenant tags
//
// Use WithTenantTags at construction time to attach a fixed tag set to every
// created secret without reusing the global TagFilter (which is a lookup
// filter, not a per-write set):
//
//	store, err := NewAWSSecretsStore(ctx, cfg,
//	    WithTenantTags(map[string]string{"tenant": tenantID}))
//
// # AWS Requirements
//
// - Valid AWS credentials configured (environment variables, profiles, or IAM role)
// - Secrets Manager service enabled in the target region
// - IAM permissions for CreateSecret, GetSecretValue, PutSecretValue, DescribeSecret, and ListSecrets
//
// # Caching
//
// The store supports optional in-memory caching to improve performance:
//
//	store, err := NewAWSSecretsStore(ctx, &AWSSecretsStoreConfig{
//	    Region:   "us-east-1",
//	    CacheTTL: 5 * time.Minute,
//	})
//
// # Thread Safety
//
// All operations are protected by internal mutexes and are safe for concurrent use.
package secrets
