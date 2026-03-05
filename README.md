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
  <a href="https://github.com/nandlabs/golly-aws/blob/main/LICENSE"><img src="https://img.shields.io/github/license/nandlabs/golly-aws?color=blue" alt="License"></a>
</p>

<p align="center">
  <a href="#installation">Installation</a> •
  <a href="#packages">Packages</a> •
  <a href="#contributing">Contributing</a>
</p>

---

## Overview

Golly AWS provides AWS service implementations for core [Golly](https://github.com/nandlabs/golly) interfaces — VFS, Messaging, and GenAI. It uses the [AWS SDK for Go v2](https://github.com/aws/aws-sdk-go-v2) and follows Golly's provider pattern: blank-import a package to auto-register it, then use standard Golly managers with `s3://`, `sqs://`, or `sns://` URLs.

## Installation

```bash
go get oss.nandlabs.io/golly-aws
```

## Packages

### ⚙️ Configuration

| Package                    | Description                                                                                           |
| -------------------------- | ----------------------------------------------------------------------------------------------------- |
| [awscfg](awscfg/README.md) | Centralized AWS config management with named registry, multi-account/region, and URL-based resolution |

### 🤖 AI & Intelligence

| Package                      | Description                                                                                          |
| ---------------------------- | ---------------------------------------------------------------------------------------------------- |
| [bedrock](bedrock/README.md) | AWS Bedrock GenAI provider using the Converse API — supports Claude, Titan, Llama, Mistral, and more |

### 🗃️ Storage

| Package            | Description                                                                                           |
| ------------------ | ----------------------------------------------------------------------------------------------------- |
| [s3](s3/README.md) | S3 implementation of the golly VFS interface — read, write, copy, move, list, walk, and directory ops |

### 📡 Messaging

| Package              | Description                                                                                 |
| -------------------- | ------------------------------------------------------------------------------------------- |
| [sns](sns/README.md) | SNS implementation of the golly messaging provider — publish, batch publish, FIFO support   |
| [sqs](sqs/README.md) | SQS implementation of the golly messaging provider — send, receive, listeners, FIFO support |

> 📖 Full API documentation available at [pkg.go.dev](https://pkg.go.dev/oss.nandlabs.io/golly-aws)

---

## Contributing

We welcome contributions to the project. If you find a bug or would like to
request a new feature, please open an issue on
[GitHub](https://github.com/nandlabs/golly-aws/issues).

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
