// Package awscfg provides a centralized AWS configuration management layer for golly-aws.
//
// It follows the same provider/manager pattern used by golly-gcp's gcpsvc package,
// allowing multiple named AWS configurations to be registered and resolved by name or URL.
//
// Usage:
//
//	cfg := awscfg.NewConfig("us-east-1")
//	cfg.SetProfile("my-profile")
//	awscfg.Manager.Register("default", cfg)
//
//	// Later, retrieve it:
//	cfg := awscfg.Manager.Get("default")
//	awsCfg, err := cfg.LoadAWSConfig(ctx)
package awscfg
