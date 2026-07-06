<h1 align="center">Golly AWS</h1>

<p align="center">
  <strong>AWS service integrations for the <a href="https://github.com/nandlabs/golly">Golly</a> ecosystem</strong>
</p>

<p align="center">
  <a href="https://goreportcard.com/report/oss.nandlabs.io/golly-aws"><img src="https://img.shields.io/badge/go%20report-A+-brightgreen.svg?style=flat" alt="Go Report"></a>
  <a href="https://github.com/nandlabs/golly-aws/actions?query=event%3Apush+branch%3Amain+"><img src="https://img.shields.io/github/actions/workflow/status/nandlabs/golly-aws/go_ci.yml?branch=main&event=push&color=228B22" alt="Build Status"></a>
  <a href="https://github.com/nandlabs/golly-aws/releases/latest"><img src="https://img.shields.io/github/v/release/nandlabs/golly-aws?label=latest&color=228B22" alt="Release"></a>
  <a href="https://github.com/nandlabs/golly-aws/releases/latest"><img src="https://img.shields.io/github/release-date/nandlabs/golly-aws?label=released&color=00ADD8" alt="Release Date"></a>
  <a href="https://pkg.go.dev/oss.nandlabs.io/golly-aws"><img src="https://godoc.org/oss.nandlabs.io/golly-aws?status.svg" alt="GoDoc"></a>
  <a href="https://github.com/nandlabs/golly-aws/blob/main/LICENSING.md"><img src="https://img.shields.io/github/license/nandlabs/golly-aws?color=blue" alt="License"></a>
</p>

<p align="center">
  <a href="#installation">Installation</a> •
  <a href="#packages">Packages</a> •
  <a href="#contributing">Contributing</a>
</p>

---

## Overview

Golly AWS provides AWS service implementations for the full set of [Golly](https://github.com/nandlabs/golly) capability interfaces — VFS, Messaging, Cache, Secrets, GenAI, Auth, Authz, Vectorstore, and Chrono leader election. It uses the [AWS SDK for Go v2](https://github.com/aws/aws-sdk-go-v2) and follows Golly's provider pattern: blank-import a package to auto-register it, then use standard Golly managers with `s3://`, `sqs://`, or `sns://` URLs.

## Compatibility

| golly-aws | golly  | AWS SDK v2 |
| --------- | ------ | ---------- |
| v1.0.0    | v1.7.0 | v1.42.1    |
| v0.3.1    | v1.5.1 | v1.41.7    |
| v0.3.0    | v1.5.0 | v1.41.6    |
| v0.2.0    | v1.4.0 | v1.41.2    |

## Installation

```bash
go get oss.nandlabs.io/golly-aws@v1.0.0
```

## Packages

### ⚙️ Configuration

| Package                    | Description                                                                                           |
| -------------------------- | ----------------------------------------------------------------------------------------------------- |
| [awscfg](awscfg/README.md) | Centralized AWS config management with named registry, multi-account/region, and URL-based resolution |

### 🔐 Auth & Authorization

| Package                                    | Description                                                                                                    |
| ------------------------------------------ | -------------------------------------------------------------------------------------------------------------- |
| [auth/cognito](auth/cognito/README.md)     | Cognito User Pool JWT verifier (JWKS-cached, id & access tokens) + DynamoDB-backed `auth.SessionStore`         |
| [authz/awsiam](authz/awsiam/README.md)     | AWS IAM `authz.Policy` — cached `iam:SimulatePrincipalPolicy` evaluator + `IAMResource{Ctx,Arn}` live-check    |

### 🤖 AI & Intelligence

| Package                                             | Description                                                                                                     |
| --------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| [bedrock](bedrock/README.md)                        | AWS Bedrock GenAI provider using the Converse API — Claude, Titan, Llama, Mistral, tool calling, and Embeddings |
| [vectorstore/opensearch](vectorstore/opensearch/README.md) | OpenSearch kNN plugin backend — `_bulk` upsert/delete + `knn` search + metadata filters                    |
| [vectorstore/bedrockkb](vectorstore/bedrockkb/README.md)   | Amazon Bedrock Knowledge Bases (read-only) — retrieve via the managed KB pipeline                          |

### 🗃️ Storage

| Package            | Description                                                                                                                                          |
| ------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| [s3](s3/README.md) | S3 implementation of the golly VFS interface — read, write, copy, move, list, walk; VFileSystemCtx, Lister, RangeReader, and sentinel-error mapping |

### 🧠 Cache

| Package                                        | Description                                                                                          |
| ---------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| [cache/elasticache](cache/elasticache/README.md) | ElastiCache Redis backend for `cache.Cache[K,V]` + `Sweeper` + `Loader` (single-flight); IAM auth |

### 🕐 Scheduling & Leader Election

| Package                                    | Description                                                                                            |
| ------------------------------------------ | ------------------------------------------------------------------------------------------------------ |
| [chrono/dynamolock](chrono/dynamolock/README.md) | `chrono.LeaderElector` on a DynamoDB item — conditional PutItem with TTL-attribute auto-cleanup |
| [chrono/s3lock](chrono/s3lock/README.md)         | `chrono.LeaderElector` on an S3 object — `If-None-Match` acquire + `If-Match` renew/steal        |

### 🔑 Secrets

| Package                      | Description                                                                                              |
| ---------------------------- | -------------------------------------------------------------------------------------------------------- |
| [secrets](secrets/README.md) | AWS Secrets Manager implementation of the golly secrets store — get, write, list; namespaced + Authorizer wiring; `WithTenantTags` |

### 📡 Messaging

| Package              | Description                                                                                                                             |
| -------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| [sns](sns/README.md) | SNS provider — publish, batch publish, FIFO `MessageGroupId` via `Keyed`, `ProducerCtx`, `ObservableProvider`, broker-targeted options   |
| [sqs](sqs/README.md) | SQS provider — send/receive/listeners, FIFO, `ListenerRemover`, `Producer/ReceiverCtx`, `Keyed → MessageGroupId`, broker-targeted options |

> 📖 Full API documentation available at [pkg.go.dev](https://pkg.go.dev/oss.nandlabs.io/golly-aws)

---

## Contributing

We welcome contributions to the project. If you find a bug or would like to
request a new feature, please open an issue on
[GitHub](https://github.com/nandlabs/golly-aws/issues).

## License

Licensed under either of

- Apache License, Version 2.0 ([LICENSE-APACHE](LICENSE-APACHE) or <http://www.apache.org/licenses/LICENSE-2.0>)
- MIT license ([LICENSE-MIT](LICENSE-MIT) or <http://opensource.org/licenses/MIT>)

at your option.

### Contribution

Unless you explicitly state otherwise, any contribution intentionally submitted
for inclusion in the work by you, as defined in the Apache-2.0 license, shall be
dual licensed as above, without any additional terms or conditions.
