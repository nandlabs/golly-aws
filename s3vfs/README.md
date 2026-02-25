# s3vfs

AWS S3 implementation of the [golly VFS](https://pkg.go.dev/oss.nandlabs.io/golly/vfs) (Virtual File System) interface.

---

- [Installation](#installation)
- [Features](#features)
- [URL Format](#url-format)
- [Configuration](#configuration)
  - [How It Works](#how-it-works)
  - [Config Options](#config-options)
  - [Setup Examples](#setup-examples)
- [Usage](#usage)
- [API Reference](#api-reference)
- [Contributing](#contributing)

---

## Installation

```bash
go get oss.nandlabs.io/golly-aws/s3vfs
```

## Features

### File Operations

- **Read** — stream object content from S3
- **Write** — buffered writes flushed to S3 on `Close()`
- **Delete** — delete a single object
- **DeleteAll** — recursively delete all objects under a prefix
- **ListAll** — list all objects under a prefix
- **Info** — get object metadata (size, last modified, content type, directory check)
- **Parent** — navigate to the parent prefix
- **AddProperty / GetProperty** — read and write custom S3 object metadata
- **ContentType** — retrieve the MIME type of the object

### File System Operations

- **Create** — create a new empty object
- **Open** — open an existing object for reading/writing
- **Mkdir / MkdirAll** — create directory markers (zero-byte objects with trailing `/`)
- **Copy** — server-side copy with stream fallback for cross-region scenarios
- **Move** — copy + delete
- **Delete** — delete object or recursively delete prefix
- **List** — list direct children of a prefix (files and common prefixes)
- **Walk** — recursively traverse all objects under a prefix
- **Find** — filter objects using a custom `FileFilter` function
- **DeleteMatching** — delete objects matching a filter

All operations also have `*Raw` variants that accept URL strings instead of `*url.URL`.

## URL Format

```
s3://bucket-name/path/to/object.txt
s3://bucket-name/path/to/folder/
```

| Component | Maps To                        |
| --------- | ------------------------------ |
| Scheme    | `s3`                           |
| Host      | S3 bucket name                 |
| Path      | Object key (prefix + filename) |

## Configuration

s3vfs uses the [`awscfg`](../awscfg/) package for AWS configuration management. At the core of this system is `awscfg.Manager` — a named registry of `*awscfg.Config` instances. You register configs under keys, and s3vfs automatically resolves the right config for each S3 URL.

### How It Works

#### 1. Registration

Before performing any S3 operations, you register one or more `*awscfg.Config` instances with `awscfg.Manager`:

```go
cfg := awscfg.NewConfig("us-east-1")
awscfg.Manager.Register("s3", cfg)
```

`awscfg.Manager` is a `managers.ItemManager[*Config]` — a typed, thread-safe, named key-value store. You can register any number of configs under different keys:

```go
awscfg.Manager.Register("s3", defaultCfg)              // fallback for all S3 ops
awscfg.Manager.Register("my-bucket", bucketSpecificCfg) // bucket-specific
awscfg.Manager.Register("logs-bucket", logsCfg)         // another bucket
```

#### 2. Resolution

When s3vfs needs an S3 client (e.g., to read `s3://my-bucket/data/file.txt`), it calls:

```go
cfg := awscfg.GetConfig(parsedURL, "s3")
```

`GetConfig` resolves the config using a **three-step fallback chain**:

| Step | Lookup Key                  | Example for `s3://my-bucket/data/file.txt` | Purpose                       |
| ---- | --------------------------- | ------------------------------------------ | ----------------------------- |
| 1    | `url.Host`                  | `Manager.Get("my-bucket")`                 | Bucket-specific config        |
| 2    | `url.Host + "/" + url.Path` | `Manager.Get("my-bucket/data/file.txt")`   | Path-specific config          |
| 3    | Fallback name (`"s3"`)      | `Manager.Get("s3")`                        | Default config for all S3 ops |

The first non-nil result is used. If all three return nil, s3vfs attempts to load the default AWS config from the environment (env vars, `~/.aws/config`, instance metadata, etc.).

#### 3. Client Creation

Once a config is resolved, s3vfs:

1. Calls `cfg.LoadAWSConfig(ctx)` to build an `aws.Config` — this applies region, profile, credentials, shared config files, and any custom load options.
2. Creates an `s3.Client` from the resulting `aws.Config`.
3. If `cfg.Endpoint` is set (e.g., for LocalStack), it also enables **path-style addressing** and sets the custom base endpoint on the client.

```
┌─────────────────┐     ┌──────────────────────┐     ┌────────────────┐
│  S3 URL          │────▶│  awscfg.GetConfig()   │────▶│  *awscfg.Config │
│  s3://bucket/key │     │  (3-step resolution)  │     │  (Region, Creds)│
└─────────────────┘     └──────────────────────┘     └───────┬────────┘
                                                              │
                                                              ▼
                                                     ┌────────────────┐
                                                     │  LoadAWSConfig() │
                                                     │  → aws.Config   │
                                                     └───────┬────────┘
                                                              │
                                                              ▼
                                                     ┌────────────────┐
                                                     │  s3.Client      │
                                                     │  (ready to use) │
                                                     └────────────────┘
```

### Config Options

`awscfg.Config` supports the following fields and setters:

| Field / Setter                                                                                 | Description                                            |
| ---------------------------------------------------------------------------------------------- | ------------------------------------------------------ |
| `Region` / `SetRegion(region)`                                                                 | AWS region (e.g., `"us-east-1"`)                       |
| `Profile` / `SetProfile(profile)`                                                              | AWS shared config profile name                         |
| `AccessKeyID`, `SecretAccessKey`, `SessionToken` / `SetStaticCredentials(akid, secret, token)` | Static IAM credentials                                 |
| `Endpoint` / `SetEndpoint(url)`                                                                | Custom endpoint URL (for LocalStack, MinIO, etc.)      |
| `SharedConfigFiles` / `SetSharedConfigFiles(files...)`                                         | Additional shared config file paths                    |
| `SharedCredentialsFiles` / `SetSharedCredentialsFiles(files...)`                               | Additional shared credentials file paths               |
| `SetCredentialFile(path)`                                                                      | Appends a credentials file (with existence check)      |
| `LoadOptions` / `AddLoadOption(fn)`                                                            | Custom `config.LoadOptions` functions for advanced use |

### Setup Examples

#### Basic Setup

```go
package main

import (
    "oss.nandlabs.io/golly-aws/awscfg"
    _ "oss.nandlabs.io/golly-aws/s3vfs" // auto-registers with VFS manager
    "oss.nandlabs.io/golly/vfs"
)

func main() {
    // Register a default AWS config for all S3 operations
    cfg := awscfg.NewConfig("us-east-1")
    awscfg.Manager.Register("s3", cfg)

    // Now use the VFS manager — s3vfs resolves config automatically
    mgr := vfs.GetManager()
    file, _ := mgr.OpenRaw("s3://my-bucket/data/file.txt")
    // ...
}
```

#### With Static Credentials

```go
cfg := awscfg.NewConfig("us-west-2")
cfg.SetStaticCredentials("AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "")
awscfg.Manager.Register("s3", cfg)
```

#### With AWS Profile

```go
cfg := awscfg.NewConfig("eu-west-1")
cfg.SetProfile("my-profile")
awscfg.Manager.Register("s3", cfg)
```

#### With Custom Endpoint (LocalStack, MinIO)

When using a custom endpoint, s3vfs automatically enables path-style addressing (`http://localhost:4566/bucket/key` instead of `http://bucket.localhost:4566/key`):

```go
cfg := awscfg.NewConfig("us-east-1")
cfg.SetEndpoint("http://localhost:4566")
cfg.SetStaticCredentials("test", "test", "")
awscfg.Manager.Register("s3", cfg)
```

#### Per-Bucket Configuration

Register different configs for different buckets. The bucket name in the S3 URL is matched against the registration key:

```go
// Default fallback for any bucket without a specific config
defaultCfg := awscfg.NewConfig("us-east-1")
awscfg.Manager.Register("s3", defaultCfg)

// Bucket-specific: production data in us-east-1 with prod profile
prodCfg := awscfg.NewConfig("us-east-1")
prodCfg.SetProfile("prod")
awscfg.Manager.Register("prod-data-bucket", prodCfg)

// Bucket-specific: EU data in eu-west-1 with separate credentials
euCfg := awscfg.NewConfig("eu-west-1")
euCfg.SetStaticCredentials("EU_AKID", "EU_SECRET", "")
awscfg.Manager.Register("eu-data-bucket", euCfg)

// Bucket-specific: dev bucket pointing to LocalStack
devCfg := awscfg.NewConfig("us-east-1")
devCfg.SetEndpoint("http://localhost:4566")
devCfg.SetStaticCredentials("test", "test", "")
awscfg.Manager.Register("dev-bucket", devCfg)
```

With the above registration:

| S3 URL                                 | Config Resolved | Region    | Why                                      |
| -------------------------------------- | --------------- | --------- | ---------------------------------------- |
| `s3://prod-data-bucket/reports/q1.csv` | `prodCfg`       | us-east-1 | Host `"prod-data-bucket"` matches step 1 |
| `s3://eu-data-bucket/logs/app.log`     | `euCfg`         | eu-west-1 | Host `"eu-data-bucket"` matches step 1   |
| `s3://dev-bucket/test/file.txt`        | `devCfg`        | us-east-1 | Host `"dev-bucket"` matches step 1       |
| `s3://any-other-bucket/data.json`      | `defaultCfg`    | us-east-1 | No host match → falls back to `"s3"`     |

#### With Custom Load Options

For advanced AWS SDK configuration, use `AddLoadOption`:

```go
cfg := awscfg.NewConfig("us-east-1")
cfg.AddLoadOption(func(o *config.LoadOptions) error {
    o.DefaultRegion = "us-west-2"
    return nil
})
awscfg.Manager.Register("s3", cfg)
```

## Usage

### Reading a File

```go
file, err := vfs.GetManager().OpenRaw("s3://my-bucket/data/report.csv")
if err != nil {
    log.Fatal(err)
}
defer file.Close()

content, err := file.AsString()
if err != nil {
    log.Fatal(err)
}
fmt.Println(content)
```

### Writing a File

```go
file, err := vfs.GetManager().CreateRaw("s3://my-bucket/output/result.json")
if err != nil {
    log.Fatal(err)
}

_, err = file.WriteString(`{"status": "ok"}`)
if err != nil {
    log.Fatal(err)
}
// Data is flushed to S3 on Close
err = file.Close()
```

### Listing Files

```go
files, err := vfs.GetManager().ListRaw("s3://my-bucket/data/")
if err != nil {
    log.Fatal(err)
}
for _, f := range files {
    info, _ := f.Info()
    fmt.Printf("%s (size: %d, dir: %t)\n", info.Name(), info.Size(), info.IsDir())
}
```

### Walking a Directory Tree

```go
err := vfs.GetManager().WalkRaw("s3://my-bucket/logs/", func(file vfs.VFile) error {
    info, _ := file.Info()
    fmt.Println(info.Name())
    return nil
})
```

### Copying Files

```go
// Server-side copy within S3
err := vfs.GetManager().CopyRaw(
    "s3://src-bucket/data/file.txt",
    "s3://dst-bucket/backup/file.txt",
)
```

### Moving Files

```go
err := vfs.GetManager().MoveRaw(
    "s3://my-bucket/temp/upload.dat",
    "s3://my-bucket/archive/upload.dat",
)
```

### Creating Directories

```go
dir, err := vfs.GetManager().MkdirRaw("s3://my-bucket/new-folder/")
if err != nil {
    log.Fatal(err)
}
defer dir.Close()
```

### Deleting Files

```go
// Delete a single file
err := vfs.GetManager().DeleteRaw("s3://my-bucket/old-file.txt")

// Delete a directory and all its contents
err = vfs.GetManager().DeleteRaw("s3://my-bucket/old-folder/")
```

### Working with Metadata

```go
file, _ := vfs.GetManager().OpenRaw("s3://my-bucket/data/report.csv")
defer file.Close()

// Add metadata
file.AddProperty("department", "engineering")

// Read metadata
dept, _ := file.GetProperty("department")
fmt.Println(dept) // "engineering"
```

### Finding Files with a Filter

```go
location, _ := url.Parse("s3://my-bucket/data/")
csvFiles, err := vfs.GetManager().Find(location, func(file vfs.VFile) (bool, error) {
    info, err := file.Info()
    if err != nil {
        return false, err
    }
    return strings.HasSuffix(info.Name(), ".csv"), nil
})
```

## API Reference

### S3FS (VFileSystem)

| Method                      | Description                                 |
| --------------------------- | ------------------------------------------- |
| `Schemes()`                 | Returns `["s3"]`                            |
| `Create(u)`                 | Creates a new empty S3 object               |
| `Open(u)`                   | Opens an S3 object (lazy — no network call) |
| `Mkdir(u)` / `MkdirAll(u)`  | Creates a directory marker                  |
| `Copy(src, dst)`            | Server-side copy with stream fallback       |
| `Move(src, dst)`            | Copy + delete                               |
| `Delete(src)`               | Delete object or recursive prefix delete    |
| `List(u)`                   | List direct children (with delimiter)       |
| `Walk(u, fn)`               | Recursive traversal of all objects          |
| `Find(u, filter)`           | Find objects matching a filter              |
| `DeleteMatching(u, filter)` | Delete objects matching a filter            |

### S3File (VFile)

| Method                 | Description                                       |
| ---------------------- | ------------------------------------------------- |
| `Read(b)`              | Streams object content from S3                    |
| `Write(b)`             | Buffers data (flushed on Close)                   |
| `Seek(offset, whence)` | Reset to start only (`SeekStart`, 0)              |
| `Close()`              | Flushes writes to S3, closes readers              |
| `ListAll()`            | Lists all objects under this prefix               |
| `Delete()`             | Deletes this object                               |
| `DeleteAll()`          | Recursively deletes all objects under this prefix |
| `Info()`               | Returns `S3FileInfo`                              |
| `Parent()`             | Returns parent prefix as `VFile`                  |
| `Url()`                | Returns the S3 URL                                |
| `ContentType()`        | Returns the MIME content type                     |
| `AddProperty(k, v)`    | Sets S3 user metadata                             |
| `GetProperty(k)`       | Gets S3 user metadata                             |
| `AsString()`           | Reads entire content as string                    |
| `AsBytes()`            | Reads entire content as byte slice                |
| `WriteString(s)`       | Writes a string to the buffer                     |

### S3FileInfo (VFileInfo)

| Method      | Description                 |
| ----------- | --------------------------- |
| `Name()`    | Object key                  |
| `Size()`    | Size in bytes               |
| `Mode()`    | Always `0` (not applicable) |
| `ModTime()` | Last modified time          |
| `IsDir()`   | `true` if prefix/directory  |
| `Sys()`     | Returns the `VFileSystem`   |

## Contributing

We welcome contributions. If you find a bug or would like to request a new feature, please open an issue on [GitHub](https://github.com/nandlabs/golly-aws/issues).
