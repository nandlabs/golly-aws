# AWS Secrets Manager Store

This package provides a Golly Store implementation backed by AWS Secrets Manager.

## Features

- **Full Credential Support**: Works with all Golly credential types
- **Automatic Secret Creation**: Automatically creates secrets if they don't exist
- **Tag Support**: Filter and organize secrets by AWS tags
- **In-Memory Caching**: Optional caching with TTL
- **JSON Storage**: Stores credentials as JSON for easy integration

## Creating an AWS Secrets Store

### Basic Configuration

```go
store, err := NewAWSSecretsStore(ctx, &AWSSecretsStoreConfig{
    Region: "us-east-1",
})
```

### With Tag Filtering

```go
store, err := NewAWSSecretsStore(ctx, &AWSSecretsStoreConfig{
    Region: "us-east-1",
    TagFilter: map[string]string{
        "app": "golly",
        "env": "production",
    },
})
```

### With Caching

```go
store, err := NewAWSSecretsStore(ctx, &AWSSecretsStoreConfig{
    Region:   "us-east-1",
    CacheTTL: 5 * time.Minute,
})
```

## Usage

### Writing a Credential

```go
cred := &secrets.Credential{
    Value:       []byte("secret-api-key"),
    LastUpdated: time.Now(),
    Version:     "1.0",
}

err := store.Write("my-api-key", cred, context.Background())
```

### Reading a Credential

```go
cred, err := store.Get("my-api-key", context.Background())
```

### Listing All Credentials

```go
keys, err := store.List(context.Background())
```

### Per-Tenant Tags on Write

`TagFilter` is a *lookup* filter — reusing it for writes leaks the same tags
across tenants. Pass `WithTenantTags` when constructing the store to attach a
distinct tag set to every secret created by that instance:

```go
tenantStore, _ := NewAWSSecretsStore(ctx, cfg,
    WithTenantTags(map[string]string{"tenant": tenantID}),
)
scoped := secrets.Namespace(tenantStore, "tenant/"+tenantID,
    secrets.WithAuthorizer(policy))
```

When `WithTenantTags` is set it replaces (does not merge with) `TagFilter` on
`CreateSecret` calls. When it is not set, the legacy `TagFilter` behavior is
preserved for backwards compatibility.

### Deleting a Credential

`AWSSecretsStore` does not expose a public `Delete` method. The upstream
`secrets.Store` interface has no `Delete`, so a public `Delete` here would
bypass `secrets.Namespaced` + `WithAuthorizer` — a caller could destroy tenant
secrets with no policy check. Once upstream lands a `Deleter` optional
interface (with an `OpDelete` authorization op), this store will implement it.
Until then, call `GetClient().DeleteSecret(...)` explicitly if you need
low-level delete access, and enforce authorization at the call site.

## IAM Policy Requirements

Minimum IAM policy for Secrets Manager operations:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "secretsmanager:CreateSecret",
        "secretsmanager:GetSecretValue",
        "secretsmanager:PutSecretValue",
        "secretsmanager:DescribeSecret",
        "secretsmanager:ListSecrets"
      ],
      "Resource": "*"
    }
  ]
}
```

For more restrictive policies, limit the Resource to specific secret ARN patterns.

## Advanced Usage

### Using the Secrets Manager Client Directly

```go
client := store.GetClient()
// Use client for advanced operations
```

### Clearing the Cache

```go
store.ClearCache()
```

## Storage Format

Credentials are stored as JSON in AWS Secrets Manager:

```json
{
  "value": "secret-value",
  "version": "1.0",
  "last_updated": 1682505600,
  "metadata": {
    "provider": "AWS",
    "region": "us-east-1"
  },
  "aws_version_id": "XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX",
  "aws_arn": "arn:aws:secretsmanager:us-east-1:123456789012:secret:my-key-XXXXX"
}
```

## Performance Considerations

- Secrets Manager API calls are rate-limited; use caching for frequent access
- Consider batch operations for multiple secrets
- Use VPC endpoints for Secrets Manager to avoid NAT gateway costs
- Enable secret replication for high-availability scenarios

## Error Handling

```go
cred, err := store.Get("nonexistent", context.Background())
if err != nil {
    if strings.Contains(err.Error(), "ResourceNotFoundException") {
        log.Println("Secret not found")
    }
}
```

## Thread Safety

The store is thread-safe for concurrent operations due to internal mutex protection.
