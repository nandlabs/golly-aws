package awscfg

import (
	"context"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"oss.nandlabs.io/golly/managers"
)

// Config holds AWS configuration options used to build an aws.Config.
type Config struct {
	// Region is the AWS region (e.g., "us-east-1").
	Region string
	// Profile is the AWS shared config profile name.
	Profile string
	// AccessKeyID for static credential authentication.
	AccessKeyID string
	// SecretAccessKey for static credential authentication.
	SecretAccessKey string
	// SessionToken for temporary credential authentication.
	SessionToken string
	// Endpoint is an optional custom endpoint URL (useful for localstack, etc.).
	Endpoint string
	// SharedConfigFiles is a list of additional shared config file paths.
	SharedConfigFiles []string
	// SharedCredentialsFiles is a list of additional shared credentials file paths.
	SharedCredentialsFiles []string
	// LoadOptions holds additional aws-sdk-go-v2/config.LoadOptions functions.
	LoadOptions []func(*config.LoadOptions) error
}

// NewConfig creates a new Config with the given region.
func NewConfig(region string) *Config {
	return &Config{
		Region: region,
	}
}

// SetRegion sets the AWS region.
func (c *Config) SetRegion(region string) {
	c.Region = region
}

// SetProfile sets the AWS shared config profile.
func (c *Config) SetProfile(profile string) {
	c.Profile = profile
}

// SetStaticCredentials sets static IAM credentials (access key, secret key, and optional session token).
func (c *Config) SetStaticCredentials(accessKeyID, secretAccessKey, sessionToken string) {
	c.AccessKeyID = accessKeyID
	c.SecretAccessKey = secretAccessKey
	c.SessionToken = sessionToken
}

// SetEndpoint sets a custom endpoint URL.
func (c *Config) SetEndpoint(endpoint string) {
	c.Endpoint = endpoint
}

// SetSharedConfigFiles sets the shared config file paths.
func (c *Config) SetSharedConfigFiles(files ...string) {
	c.SharedConfigFiles = files
}

// SetSharedCredentialsFiles sets the shared credentials file paths.
func (c *Config) SetSharedCredentialsFiles(files ...string) {
	c.SharedCredentialsFiles = files
}

// AddLoadOption appends a custom config.LoadOptions function.
func (c *Config) AddLoadOption(opt func(*config.LoadOptions) error) {
	c.LoadOptions = append(c.LoadOptions, opt)
}

// SetCredentialFile adds the given file path as a shared credentials file.
func (c *Config) SetCredentialFile(filePath string) {
	if _, err := os.Stat(filePath); err == nil {
		c.SharedCredentialsFiles = append(c.SharedCredentialsFiles, filePath)
	}
}

// LoadAWSConfig builds and returns an aws.Config using the configured options.
// It applies region, profile, credentials, endpoint, and any additional load options.
func (c *Config) LoadAWSConfig(ctx context.Context) (aws.Config, error) {
	opts := make([]func(*config.LoadOptions) error, 0, len(c.LoadOptions)+5)

	if c.Region != "" {
		opts = append(opts, config.WithRegion(c.Region))
	}

	if c.Profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(c.Profile))
	}

	if c.AccessKeyID != "" && c.SecretAccessKey != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, c.SessionToken),
		))
	}

	if len(c.SharedConfigFiles) > 0 {
		opts = append(opts, config.WithSharedConfigFiles(c.SharedConfigFiles))
	}

	if len(c.SharedCredentialsFiles) > 0 {
		opts = append(opts, config.WithSharedCredentialsFiles(c.SharedCredentialsFiles))
	}

	// Append any custom load options provided by the caller.
	opts = append(opts, c.LoadOptions...)

	return config.LoadDefaultConfig(ctx, opts...)
}

// Manager is an item manager for Config instances, allowing named registration and retrieval.
var Manager = managers.NewItemManager[*Config]()
