# awscfg

Centralized AWS configuration management for [golly-aws](https://github.com/nandlabs/golly-aws). It provides a named registry of AWS configurations that can be resolved by name or URL, enabling multi-account, multi-region, and per-resource setups.

---

- [Installation](#installation)
- [Overview](#overview)
- [Config](#config)
  - [Creating a Config](#creating-a-config)
  - [Config Fields and Setters](#config-fields-and-setters)
  - [Loading an aws.Config](#loading-an-awsconfig)
- [Manager](#manager)
  - [Registering Configs](#registering-configs)
  - [Retrieving Configs](#retrieving-configs)
- [GetConfig — URL-Based Resolution](#getconfig--url-based-resolution)
  - [Resolution Order](#resolution-order)
  - [Resolution Examples](#resolution-examples)
- [Usage Patterns](#usage-patterns)
  - [Basic Setup](#basic-setup)
  - [Static Credentials](#static-credentials)
  - [AWS Profile](#aws-profile)
  - [Custom Endpoint (LocalStack / MinIO)](#custom-endpoint-localstack--minio)
  - [Per-Bucket / Per-Resource Config](#per-bucket--per-resource-config)
  - [Shared Credentials and Config Files](#shared-credentials-and-config-files)
  - [Custom Load Options](#custom-load-options)
- [How Other Packages Use awscfg](#how-other-packages-use-awscfg)
- [API Reference](#api-reference)
- [Contributing](#contributing)

---

## Installation

```bash
go get oss.nandlabs.io/golly-aws/awscfg
```

## Overview

`awscfg` follows the same provider/manager pattern used by golly-gcp's `gcpsvc` package. The key components are:

| Component   | Description                                                               |
| ----------- | ------------------------------------------------------------------------- |
| `Config`    | Holds all AWS configuration options (region, credentials, endpoint, etc.) |
| `Manager`   | A thread-safe, named registry of `*Config` instances                      |
| `GetConfig` | Resolves a `*Config` by URL with a multi-step fallback chain              |

```
┌──────────────────┐      Register       ┌─────────────┐
│  Your Application │ ──────────────────▶ │   Manager    │
│  (setup code)     │                     │  (registry)  │
└──────────────────┘                     └──────┬──────┘
                                                │
                                          GetConfig(url, name)
                                                │
                                                ▼
┌──────────────────┐      LoadAWSConfig  ┌─────────────┐
│  AWS SDK Client   │ ◀──────────────── │   *Config    │
│  (s3, sqs, etc.)  │                     │  (resolved)  │
└──────────────────┘                     └─────────────┘
```

## Config

### Creating a Config

```go
cfg := awscfg.NewConfig("us-east-1")
```

This creates a `*Config` with the region set. All other fields are zero-valued and can be configured via setters.

### Config Fields and Setters

| Field                    | Setter                                              | Description                                                  |
| ------------------------ | --------------------------------------------------- | ------------------------------------------------------------ |
| `Region`                 | `SetRegion(region string)`                          | AWS region (e.g., `"us-east-1"`)                             |
| `Profile`                | `SetProfile(profile string)`                        | AWS shared config profile name                               |
| `AccessKeyID`            | `SetStaticCredentials(akid, secret, token string)`  | Access key for static IAM credentials                        |
| `SecretAccessKey`        | (set via `SetStaticCredentials`)                    | Secret key for static IAM credentials                        |
| `SessionToken`           | (set via `SetStaticCredentials`)                    | Session token for temporary credentials                      |
| `Endpoint`               | `SetEndpoint(endpoint string)`                      | Custom endpoint URL (for LocalStack, MinIO, etc.)            |
| `SharedConfigFiles`      | `SetSharedConfigFiles(files ...string)`             | Additional shared config file paths                          |
| `SharedCredentialsFiles` | `SetSharedCredentialsFiles(files ...string)`        | Additional shared credentials file paths                     |
| —                        | `SetCredentialFile(filePath string)`                | Appends a credentials file (only if the file exists on disk) |
| `LoadOptions`            | `AddLoadOption(fn func(*config.LoadOptions) error)` | Custom AWS SDK `config.LoadOptions` for advanced use         |

### Loading an aws.Config

Once configured, call `LoadAWSConfig` to produce a standard `aws.Config` that can be passed to any AWS SDK client:

```go
awsCfg, err := cfg.LoadAWSConfig(context.Background())
if err != nil {
    log.Fatal(err)
}

// Use awsCfg with any AWS SDK v2 client
s3Client := s3.NewFromConfig(awsCfg)
sqsClient := sqs.NewFromConfig(awsCfg)
```

`LoadAWSConfig` applies options in this order:

1. Region
2. Shared config profile
3. Static credentials (if both access key and secret key are set)
4. Shared config files
5. Shared credentials files
6. Any custom `LoadOptions` added via `AddLoadOption`

It then delegates to `config.LoadDefaultConfig`, which also picks up environment variables (`AWS_REGION`, `AWS_ACCESS_KEY_ID`, etc.), EC2 instance metadata, ECS task roles, and other standard credential sources.

## Manager

`Manager` is a package-level variable of type `managers.ItemManager[*Config]` — a typed, thread-safe, named key-value store from the golly `managers` package.

### Registering Configs

```go
// Register a default config
awscfg.Manager.Register("s3", cfg)

// Register bucket-specific configs
awscfg.Manager.Register("prod-bucket", prodCfg)
awscfg.Manager.Register("dev-bucket", devCfg)

// Register configs for other AWS services
awscfg.Manager.Register("sqs", sqsCfg)
awscfg.Manager.Register("sns", snsCfg)
```

The key is an arbitrary string. By convention:

- **Service scheme** (`"s3"`, `"sqs"`, `"sns"`) — used as the fallback for that service
- **Resource name** (`"my-bucket"`, `"my-queue"`) — used for per-resource configs

### Retrieving Configs

```go
// Direct retrieval by name
cfg := awscfg.Manager.Get("s3")

// URL-based resolution (preferred — used by sub-packages automatically)
cfg := awscfg.GetConfig(parsedURL, "s3")
```

## GetConfig — URL-Based Resolution

`GetConfig` is the primary way sub-packages (like `s3vfs`, `sqs`, `sns`) resolve configurations. It takes a `*url.URL` and a fallback name, and returns the best-matching `*Config`.

### Resolution Order

```go
func GetConfig(u *url.URL, name string) *Config
```

| Step | Lookup Key              | Description                           |
| ---- | ----------------------- | ------------------------------------- |
| 1    | `u.Host`                | Resource-specific (e.g., bucket name) |
| 2    | `u.Host + "/" + u.Path` | Path-specific (most granular)         |
| 3    | `name`                  | Fallback (e.g., `"s3"`)               |

If `u` is nil, it skips directly to `Manager.Get(name)`.  
If all steps return nil, the caller receives nil and must handle the fallback (e.g., loading default AWS config from the environment).

### Resolution Examples

Given these registrations:

```go
awscfg.Manager.Register("s3", defaultCfg)
awscfg.Manager.Register("prod-bucket", prodCfg)
awscfg.Manager.Register("eu-bucket", euCfg)
```

| URL                              | Step 1: Host           | Step 2: Host+Path               | Step 3: Name | Result       |
| -------------------------------- | ---------------------- | ------------------------------- | ------------ | ------------ |
| `s3://prod-bucket/data/file.txt` | `"prod-bucket"` → hit  | —                               | —            | `prodCfg`    |
| `s3://eu-bucket/logs/app.log`    | `"eu-bucket"` → hit    | —                               | —            | `euCfg`      |
| `s3://other-bucket/file.txt`     | `"other-bucket"` → nil | `"other-bucket/file.txt"` → nil | `"s3"` → hit | `defaultCfg` |
| nil URL, name=`"s3"`             | (skipped)              | (skipped)                       | `"s3"` → hit | `defaultCfg` |

## Usage Patterns

### Basic Setup

```go
package main

import (
    "context"
    "fmt"
    "oss.nandlabs.io/golly-aws/awscfg"
)

func main() {
    cfg := awscfg.NewConfig("us-east-1")
    awscfg.Manager.Register("s3", cfg)

    // Later, any code can resolve it:
    resolved := awscfg.Manager.Get("s3")
    awsCfg, err := resolved.LoadAWSConfig(context.Background())
    if err != nil {
        panic(err)
    }
    fmt.Println("Region:", awsCfg.Region)
}
```

### Static Credentials

```go
cfg := awscfg.NewConfig("us-west-2")
cfg.SetStaticCredentials("AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "")
awscfg.Manager.Register("s3", cfg)
```

### AWS Profile

```go
cfg := awscfg.NewConfig("eu-west-1")
cfg.SetProfile("my-profile")
awscfg.Manager.Register("s3", cfg)
```

### Custom Endpoint (LocalStack / MinIO)

```go
cfg := awscfg.NewConfig("us-east-1")
cfg.SetEndpoint("http://localhost:4566")
cfg.SetStaticCredentials("test", "test", "")
awscfg.Manager.Register("s3", cfg)
```

> **Note:** The `Endpoint` field is stored on `Config` and consumed by sub-packages (e.g., `s3vfs`) when creating service clients. `LoadAWSConfig` itself does not inject the endpoint — this is intentional because endpoint configuration is service-specific in AWS SDK v2.

### Per-Bucket / Per-Resource Config

```go
// Default for all S3 operations
defaultCfg := awscfg.NewConfig("us-east-1")
awscfg.Manager.Register("s3", defaultCfg)

// Production bucket with a specific profile
prodCfg := awscfg.NewConfig("us-east-1")
prodCfg.SetProfile("prod")
awscfg.Manager.Register("prod-data-bucket", prodCfg)

// EU bucket with separate credentials and region
euCfg := awscfg.NewConfig("eu-west-1")
euCfg.SetStaticCredentials("EU_AKID", "EU_SECRET", "")
awscfg.Manager.Register("eu-data-bucket", euCfg)
```

Now `s3://prod-data-bucket/...` automatically uses the prod profile, `s3://eu-data-bucket/...` uses EU credentials, and any other bucket falls back to the default config.

### Shared Credentials and Config Files

```go
cfg := awscfg.NewConfig("us-east-1")
cfg.SetSharedConfigFiles("/etc/aws/config", "/app/aws-config")
cfg.SetSharedCredentialsFiles("/etc/aws/credentials")
awscfg.Manager.Register("s3", cfg)

// Or add a single credentials file (with existence check):
cfg.SetCredentialFile("/home/app/.aws/credentials")
```

### Custom Load Options

For advanced AWS SDK configuration not covered by the built-in setters:

```go
cfg := awscfg.NewConfig("us-east-1")
cfg.AddLoadOption(func(o *config.LoadOptions) error {
    o.DefaultRegion = "us-west-2"
    return nil
})
awscfg.Manager.Register("s3", cfg)
```

## How Other Packages Use awscfg

Sub-packages in golly-aws (`s3vfs`, `sqs`, `sns`, etc.) use `awscfg.GetConfig` to resolve configuration automatically when handling URLs:

```go
// Inside s3vfs/pkg.go
func getS3Client(opts *urlOpts) (*s3.Client, error) {
    cfg := awscfg.GetConfig(opts.u, "s3")     // ← resolves config
    if cfg == nil {
        // fallback: load default AWS config from environment
        awsCfg, err := (&awscfg.Config{}).LoadAWSConfig(ctx)
        return s3.NewFromConfig(awsCfg), nil
    }

    awsCfg, err := cfg.LoadAWSConfig(ctx)     // ← builds aws.Config
    return s3.NewFromConfig(awsCfg, ...), nil  // ← creates S3 client
}
```

This means your application only needs to register configs once at startup, and all sub-packages will resolve the correct config for each URL automatically.

## API Reference

### Config

| Method / Field              | Signature                                                                 | Description                                    |
| --------------------------- | ------------------------------------------------------------------------- | ---------------------------------------------- |
| `NewConfig`                 | `func NewConfig(region string) *Config`                                   | Creates a new Config with the given region     |
| `SetRegion`                 | `func (c *Config) SetRegion(region string)`                               | Sets the AWS region                            |
| `SetProfile`                | `func (c *Config) SetProfile(profile string)`                             | Sets the shared config profile                 |
| `SetStaticCredentials`      | `func (c *Config) SetStaticCredentials(akid, secret, token string)`       | Sets static IAM credentials                    |
| `SetEndpoint`               | `func (c *Config) SetEndpoint(endpoint string)`                           | Sets a custom endpoint URL                     |
| `SetSharedConfigFiles`      | `func (c *Config) SetSharedConfigFiles(files ...string)`                  | Sets shared config file paths                  |
| `SetSharedCredentialsFiles` | `func (c *Config) SetSharedCredentialsFiles(files ...string)`             | Sets shared credentials file paths             |
| `SetCredentialFile`         | `func (c *Config) SetCredentialFile(filePath string)`                     | Appends a credentials file (existence checked) |
| `AddLoadOption`             | `func (c *Config) AddLoadOption(fn func(*config.LoadOptions) error)`      | Adds a custom AWS SDK load option              |
| `LoadAWSConfig`             | `func (c *Config) LoadAWSConfig(ctx context.Context) (aws.Config, error)` | Builds a standard `aws.Config`                 |

### Manager

| Usage                         | Description                                      |
| ----------------------------- | ------------------------------------------------ |
| `Manager.Register(name, cfg)` | Stores a `*Config` under the given name          |
| `Manager.Get(name)`           | Retrieves a `*Config` by name (nil if not found) |

### GetConfig

| Usage                                | Description                                                   |
| ------------------------------------ | ------------------------------------------------------------- |
| `GetConfig(u *url.URL, name string)` | Resolves a `*Config` via URL host → host+path → fallback name |

## Contributing

We welcome contributions. If you find a bug or would like to request a new feature, please open an issue on [GitHub](https://github.com/nandlabs/golly-aws/issues).
