package awscfg

import (
	"context"
	"net/url"
	"testing"
)

func TestNewConfig(t *testing.T) {
	cfg := NewConfig("us-east-1")
	if cfg.Region != "us-east-1" {
		t.Errorf("expected region us-east-1, got %s", cfg.Region)
	}
}

func TestSetRegion(t *testing.T) {
	cfg := NewConfig("us-east-1")
	cfg.SetRegion("eu-west-1")
	if cfg.Region != "eu-west-1" {
		t.Errorf("expected region eu-west-1, got %s", cfg.Region)
	}
}

func TestSetProfile(t *testing.T) {
	cfg := NewConfig("us-east-1")
	cfg.SetProfile("my-profile")
	if cfg.Profile != "my-profile" {
		t.Errorf("expected profile my-profile, got %s", cfg.Profile)
	}
}

func TestSetStaticCredentials(t *testing.T) {
	cfg := NewConfig("us-east-1")
	cfg.SetStaticCredentials("AKID", "SECRET", "TOKEN")
	if cfg.AccessKeyID != "AKID" {
		t.Errorf("expected access key AKID, got %s", cfg.AccessKeyID)
	}
	if cfg.SecretAccessKey != "SECRET" {
		t.Errorf("expected secret key SECRET, got %s", cfg.SecretAccessKey)
	}
	if cfg.SessionToken != "TOKEN" {
		t.Errorf("expected session token TOKEN, got %s", cfg.SessionToken)
	}
}

func TestSetEndpoint(t *testing.T) {
	cfg := NewConfig("us-east-1")
	cfg.SetEndpoint("http://localhost:4566")
	if cfg.Endpoint != "http://localhost:4566" {
		t.Errorf("expected endpoint http://localhost:4566, got %s", cfg.Endpoint)
	}
}

func TestLoadAWSConfig(t *testing.T) {
	cfg := NewConfig("us-west-2")
	cfg.SetStaticCredentials("AKID", "SECRET", "")

	awsCfg, err := cfg.LoadAWSConfig(context.Background())
	if err != nil {
		t.Fatalf("unexpected error loading config: %v", err)
	}
	if awsCfg.Region != "us-west-2" {
		t.Errorf("expected region us-west-2, got %s", awsCfg.Region)
	}
}

func TestManagerRegisterAndGet(t *testing.T) {
	cfg := NewConfig("ap-south-1")
	Manager.Register("test-svc", cfg)
	defer Manager.Unregister("test-svc")

	got := Manager.Get("test-svc")
	if got == nil {
		t.Fatal("expected config, got nil")
	}
	if got.Region != "ap-south-1" {
		t.Errorf("expected region ap-south-1, got %s", got.Region)
	}
}

func TestGetConfigWithNilURL(t *testing.T) {
	cfg := NewConfig("us-east-1")
	Manager.Register("s3", cfg)
	defer Manager.Unregister("s3")

	got := GetConfig(nil, "s3")
	if got == nil {
		t.Fatal("expected config from fallback name, got nil")
	}
	if got.Region != "us-east-1" {
		t.Errorf("expected region us-east-1, got %s", got.Region)
	}
}

func TestGetConfigWithURLHost(t *testing.T) {
	cfg := NewConfig("eu-west-1")
	Manager.Register("my-bucket", cfg)
	defer Manager.Unregister("my-bucket")

	u, _ := url.Parse("s3://my-bucket/some/key")
	got := GetConfig(u, "fallback")
	if got == nil {
		t.Fatal("expected config from URL host, got nil")
	}
	if got.Region != "eu-west-1" {
		t.Errorf("expected region eu-west-1, got %s", got.Region)
	}
}

func TestGetConfigFallback(t *testing.T) {
	cfg := NewConfig("ap-southeast-1")
	Manager.Register("fallback-key", cfg)
	defer Manager.Unregister("fallback-key")

	u, _ := url.Parse("s3://unknown-host/path")
	got := GetConfig(u, "fallback-key")
	if got == nil {
		t.Fatal("expected config from fallback, got nil")
	}
	if got.Region != "ap-southeast-1" {
		t.Errorf("expected region ap-southeast-1, got %s", got.Region)
	}
}
