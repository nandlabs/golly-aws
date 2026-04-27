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
// Delete a credential:
//
//	err := store.Delete("my-secret", ctx)
//
// List all credentials:
//
//	keys, err := store.List(ctx)
//
// # AWS Requirements
//
// - Valid AWS credentials configured (environment variables, profiles, or IAM role)
// - Secrets Manager service enabled in the target region
// - IAM permissions for CreateSecret, GetSecretValue, PutSecretValue, DeleteSecret, and ListSecrets
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
