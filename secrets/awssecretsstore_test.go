package secrets

import (
	"context"
	"testing"
	"time"

	"oss.nandlabs.io/golly/secrets"
)

func TestNewAWSSecretsStore(t *testing.T) {
	// Note: This test requires AWS credentials to be configured
	// For unit testing, you would typically use mocks

	ctx := context.Background()

	// Test with default config
	cfg := &AWSSecretsStoreConfig{
		Region: "us-east-1",
	}

	store, err := NewAWSSecretsStore(ctx, cfg)
	if err != nil {
		// This is expected if AWS credentials are not configured
		t.Logf("Skipping AWS test (credentials not configured): %v", err)
		return
	}

	if store == nil {
		t.Fatal("Expected non-nil store")
	}

	if store.region != "us-east-1" {
		t.Errorf("Expected region us-east-1, got %s", store.region)
	}
}

func TestAWSSecretsStore_DefaultConfig(t *testing.T) {
	ctx := context.Background()

	store, err := NewAWSSecretsStore(ctx, nil)
	if err != nil {
		t.Logf("Skipping test (AWS not available): %v", err)
		return
	}

	if store.region != "us-east-1" {
		t.Errorf("Expected default region us-east-1, got %s", store.region)
	}
}

func TestAWSSecretsStore_Provider(t *testing.T) {
	ctx := context.Background()

	store, err := NewAWSSecretsStore(ctx, &AWSSecretsStoreConfig{
		Region: "us-west-2",
	})
	if err != nil {
		t.Logf("Skipping test (AWS not available): %v", err)
		return
	}

	if got := store.Provider(); got != AWSSecretsManagerProvider {
		t.Errorf("Provider() = %q, want %q", got, AWSSecretsManagerProvider)
	}
}

func TestAWSSecretsStore_ClearCache(t *testing.T) {
	ctx := context.Background()

	store, err := NewAWSSecretsStore(ctx, &AWSSecretsStoreConfig{
		CacheTTL: 5 * time.Minute,
	})
	if err != nil {
		t.Logf("Skipping test (AWS not available): %v", err)
		return
	}

	// Add dummy credential to cache
	store.cache["test-key"] = &secrets.Credential{
		Value:   []byte("test"),
		Version: "1.0",
	}

	if len(store.cache) == 0 {
		t.Error("Expected cache to have entries")
	}

	store.ClearCache()

	if len(store.cache) != 0 {
		t.Error("Expected cache to be empty after clearing")
	}
}

func TestAWSSecretsStore_GetClient(t *testing.T) {
	ctx := context.Background()

	store, err := NewAWSSecretsStore(ctx, &AWSSecretsStoreConfig{})
	if err != nil {
		t.Logf("Skipping test (AWS not available): %v", err)
		return
	}

	client := store.GetClient()
	if client == nil {
		t.Error("GetClient() returned nil")
	}
}

func TestAWSSecretsStore_WithTagFilter(t *testing.T) {
	ctx := context.Background()

	tagFilter := map[string]string{
		"app": "golly",
		"env": "test",
	}

	store, err := NewAWSSecretsStore(ctx, &AWSSecretsStoreConfig{
		Region:    "us-east-1",
		TagFilter: tagFilter,
	})
	if err != nil {
		t.Logf("Skipping test (AWS not available): %v", err)
		return
	}

	if len(store.tagFilter) != len(tagFilter) {
		t.Errorf("Expected %d tags, got %d", len(tagFilter), len(store.tagFilter))
	}

	if store.tagFilter["app"] != "golly" {
		t.Errorf("Expected tag app=golly, got %s", store.tagFilter["app"])
	}
}
