# sns

AWS SNS implementation of the [golly messaging](https://pkg.go.dev/oss.nandlabs.io/golly/messaging) `Provider` interface.

> **SNS is a publish-only service.** `Receive`, `ReceiveBatch`, and `AddListener` are not supported.
> For receiving messages, use an SNS→SQS subscription with the [`sqs`](../sqs/) package.

---

- [Installation](#installation)
- [Features](#features)
- [Architecture](#architecture)
- [Auto-Registration](#auto-registration)
- [URL Format](#url-format)
- [Configuration](#configuration)
- [Topic ARN Resolution](#topic-arn-resolution)
- [Usage](#usage)
- [Options](#options)
- [FIFO Topic Support](#fifo-topic-support)
- [Error Handling](#error-handling)
- [API Reference](#api-reference)
- [Prerequisites](#prerequisites)
- [Contributing](#contributing)

---

## Installation

```bash
go get oss.nandlabs.io/golly-aws/sns
```

## Features

- **Send** — publish a single message to an SNS topic, phone number, or endpoint ARN
- **SendBatch** — publish up to N messages, automatically split into batches of 10 (SNS limit)
- **Subject** — optional subject for email/email-json subscriptions
- **Per-protocol messaging** — `MessageStructure=json` for different payloads per protocol
- **SMS direct publish** — send SMS directly to a phone number without a topic
- **FIFO support** — message group ID and deduplication ID via options
- **Custom endpoint** — works with LocalStack, Moto, and other SNS-compatible services
- **Auto-registration** — blank import registers the SNS provider with the golly messaging manager
- **Config resolution** — leverages `awscfg` for per-topic or global AWS configuration
- **Lightweight** — no background goroutines, no state to manage

## Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│  Application                                                     │
│                                                                  │
│  import _ "oss.nandlabs.io/golly-aws/sns"                       │
│                                                                  │
│  mgr := messaging.GetManager()                                  │
│  mgr.Send(url, msg, opts...)                                    │
│  mgr.SendBatch(url, msgs, opts...)                              │
└─────────────────────────┬────────────────────────────────────────┘
                          │
                          ▼
┌──────────────────────────────────────────────────────────────────┐
│  golly/messaging.Manager                                         │
│                                                                  │
│  Routes to provider by URL scheme ("sns")                        │
│  Calls provider.Send / SendBatch                                │
└─────────────────────────┬────────────────────────────────────────┘
                          │
                          ▼
┌──────────────────────────────────────────────────────────────────┐
│  sns.Provider                                                    │
│                                                                  │
│  1. getSNSClient(u)         → awscfg.GetConfig(u, "sns")       │
│  2. resolveTopicARN(c, u)   → CreateTopic (idempotent) or ARN  │
│  3. SNS API call            → Publish / PublishBatch            │
└─────────────────────────┬────────────────────────────────────────┘
                          │
                          ▼
┌──────────────────────────────────────────────────────────────────┐
│  awscfg.Manager                                                  │
│                                                                  │
│  Config resolution chain:                                        │
│  url.Host → url.Host+"/"+url.Path → fallback name ("sns")      │
│                                                                  │
│  Returns *awscfg.Config → LoadAWSConfig() → aws.Config          │
└──────────────────────────────────────────────────────────────────┘
```

## Auto-Registration

On package import, the `init()` function in `pkg.go` creates a `Provider` and registers it with `messaging.GetManager()`:

```go
func init() {
    provider := &Provider{}
    messagingManager := messaging.GetManager()
    messagingManager.Register(provider)
}
```

This means a **blank import** is all you need to make the SNS provider available:

```go
import _ "oss.nandlabs.io/golly-aws/sns"
```

After this import, any call to `messaging.GetManager().Send(u, ...)` with an `sns://` scheme URL will automatically route to this provider.

## URL Format

```
sns://topic-name
sns:///arn:aws:sns:region:account-id:topic-name
```

| Component | Maps To                                                          |
| --------- | ---------------------------------------------------------------- |
| Scheme    | `sns` — used to route to this provider via the messaging manager |
| Host      | SNS topic name (e.g., `my-topic` or `my-topic.fifo`)             |
| Path      | For direct ARN usage: the full ARN (when host is empty)          |

**Examples:**

| URL                                                  | Resolution                                  |
| ---------------------------------------------------- | ------------------------------------------- |
| `sns://my-topic`                                     | Resolves ARN via `CreateTopic` (idempotent) |
| `sns:///arn:aws:sns:us-east-1:123456789012:my-topic` | Uses the ARN directly from path             |
| `sns://my-topic.fifo`                                | Resolves ARN for a FIFO topic               |

## Configuration

Configuration is resolved via the [`awscfg`](../awscfg/) package. See the [awscfg README](../awscfg/README.md) for the full resolution mechanism.

### How Config Resolution Works

When `getSNSClient` is called with a URL like `sns://my-topic`, the provider calls `awscfg.GetConfig(u, "sns")` which tries the following resolution chain:

1. **`url.Host`** — look up `"my-topic"` in `awscfg.Manager`
2. **`url.Host + "/" + url.Path`** — look up `"my-topic/"` (if path is present)
3. **Fallback name** — look up `"sns"` in `awscfg.Manager`
4. **Default AWS config** — if no awscfg entry is found at all, loads the default AWS SDK config from environment/shared credentials

### Basic Setup

```go
import (
    "oss.nandlabs.io/golly-aws/awscfg"
    _ "oss.nandlabs.io/golly-aws/sns"
    "oss.nandlabs.io/golly/messaging"
)

func main() {
    // Register a default config for all SNS operations
    cfg := awscfg.NewConfig("us-east-1")
    awscfg.Manager.Register("sns", cfg)

    mgr := messaging.GetManager()
    // mgr.Send, mgr.SendBatch, etc.
}
```

### With LocalStack / Custom Endpoint

```go
cfg := awscfg.NewConfig("us-east-1")
cfg.SetEndpoint("http://localhost:4566")
cfg.SetStaticCredentials("test", "test", "")
awscfg.Manager.Register("sns", cfg)
```

### Per-Topic Configuration

```go
// Critical alerts topic uses a specific profile
alertsCfg := awscfg.NewConfig("us-east-1")
alertsCfg.SetProfile("prod-alerts")
awscfg.Manager.Register("critical-alerts", alertsCfg)

// Default for all other SNS topics
defaultCfg := awscfg.NewConfig("us-east-1")
awscfg.Manager.Register("sns", defaultCfg)
```

**Resolution table for this setup:**

| URL                     | Resolved Config Key |
| ----------------------- | ------------------- |
| `sns://critical-alerts` | `critical-alerts`   |
| `sns://any-other-topic` | `sns` (fallback)    |

## Topic ARN Resolution

The provider must resolve a topic name to an SNS ARN before publishing. This is handled by `resolveTopicARN`:

### Direct ARN in Path

When the URL path starts with `arn:`, it is used directly without any API call:

```
Input:  sns:///arn:aws:sns:us-east-1:123456789012:my-topic
Result: arn:aws:sns:us-east-1:123456789012:my-topic
```

### Topic Name via CreateTopic

When the URL host is a plain topic name, the provider calls [`CreateTopic`](https://docs.aws.amazon.com/sns/latest/api/API_CreateTopic.html), which is **idempotent** — it returns the ARN of an existing topic, or creates a new one:

```
Input:  sns://my-topic
Action: CreateTopic(Name="my-topic")
Result: arn:aws:sns:us-east-1:123456789012:my-topic
```

### Alternative Targets

Instead of topic ARN resolution, you can publish directly to:

- **Phone number**: Use `OptPhoneNumber` option → bypasses topic ARN resolution entirely
- **Endpoint ARN**: Use `OptTargetArn` option → publishes to a specific subscription endpoint

## Usage

### Sending a Message

```go
mgr := messaging.GetManager()
u, _ := url.Parse("sns://my-topic")

msg, _ := mgr.NewMessage("sns")
_ = msg.SetBodyStr("Hello from golly SNS!")
msg.SetStrHeader("source", "my-service")

err := mgr.Send(u, msg)
if err != nil {
    log.Fatal(err)
}
```

### Sending with a Subject

```go
u, _ := url.Parse("sns://notifications")

opts := messaging.NewOptionsBuilder().
    Add("Subject", "Order Shipped").
    Build()

msg, _ := mgr.NewMessage("sns")
_ = msg.SetBodyStr("Your order #12345 has shipped!")

err := mgr.Send(u, msg, opts...)
```

### Sending a Batch

```go
u, _ := url.Parse("sns://events")

var msgs []messaging.Message
for i := 0; i < 25; i++ {
    msg, _ := mgr.NewMessage("sns")
    _, _ = msg.SetBodyStr(fmt.Sprintf("Event %d", i))
    msgs = append(msgs, msg)
}

// Automatically splits into batches of 10:
// batch 1: msgs[0:10], batch 2: msgs[10:20], batch 3: msgs[20:25]
err := mgr.SendBatch(u, msgs)
```

If any message in a batch fails, the error from the first failure is returned.

### Sending SMS (Direct Phone Number)

```go
opts := messaging.NewOptionsBuilder().
    Add("PhoneNumber", "+15551234567").
    Build()

msg, _ := mgr.NewMessage("sns")
_, _ = msg.SetBodyStr("Your verification code is 123456")

// URL can be a dummy sns:// URL — the phone number takes precedence
u, _ := url.Parse("sns://sms")
err := mgr.Send(u, msg, opts...)
```

### Per-Protocol Messages (MessageStructure)

```go
opts := messaging.NewOptionsBuilder().
    Add("MessageStructure", "json").
    Build()

msg, _ := mgr.NewMessage("sns")
_, _ = msg.SetBodyStr(`{
    "default": "Default notification",
    "sqs": "{\"event\":\"order.shipped\",\"orderId\":\"12345\"}",
    "email": "Your order #12345 has been shipped!",
    "sms": "Order #12345 shipped"
}`)

u, _ := url.Parse("sns://order-events")
err := mgr.Send(u, msg, opts...)
```

### Using Direct ARN

```go
u, _ := url.Parse("sns:///arn:aws:sns:us-east-1:123456789012:my-topic")

msg, _ := mgr.NewMessage("sns")
_, _ = msg.SetBodyStr("Direct ARN publish")

err := mgr.Send(u, msg)
```

### Accessing SNS Message ID

```go
msg, _ := mgr.NewMessage("sns")
_, _ = msg.SetBodyStr("Hello")

err := mgr.Send(u, msg)
if err == nil {
    // After a successful publish, the SNS message ID is stored on the message
    if snsMsg, ok := msg.(*sns.MessageSNS); ok {
        fmt.Println("SNS MessageId:", snsMsg.SNSMessageId())
    }
}
```

## Options

Options are passed via `messaging.Option` using the `OptionsBuilder`:

```go
opts := messaging.NewOptionsBuilder().
    Add("Subject", "Alert").
    Add("MessageGroupId", "group-1").
    Build()
```

| Key                      | Type     | Applies To      | Description                                                 |
| ------------------------ | -------- | --------------- | ----------------------------------------------------------- |
| `Subject`                | `string` | Send            | Subject line for email/email-json subscriptions             |
| `MessageGroupId`         | `string` | Send, SendBatch | Message group ID (required for FIFO topics)                 |
| `MessageDeduplicationId` | `string` | Send, SendBatch | Deduplication ID for FIFO topics                            |
| `MessageStructure`       | `string` | Send, SendBatch | Set to `"json"` for per-protocol message formatting         |
| `PhoneNumber`            | `string` | Send            | Publish SMS directly to a phone number (bypasses topic ARN) |
| `TargetArn`              | `string` | Send            | Publish to a specific subscription endpoint ARN             |

## FIFO Topic Support

For FIFO topics (topic names ending in `.fifo`), you **must** provide a `MessageGroupId`:

```go
u, _ := url.Parse("sns://order-events.fifo")

opts := messaging.NewOptionsBuilder().
    Add("MessageGroupId", "order-processing").
    Add("MessageDeduplicationId", "order-12345").
    Build()

msg, _ := mgr.NewMessage("sns")
_, _ = msg.SetBodyStr(`{"orderId": "12345", "status": "shipped"}`)

err := mgr.Send(u, msg, opts...)
```

**FIFO batch sends** apply the same `MessageGroupId` and `MessageDeduplicationId` to all messages in the batch. For per-message deduplication, send messages individually.

## Error Handling

All provider methods return descriptive errors prefixed with `sns:`:

| Error                                          | When                                                   |
| ---------------------------------------------- | ------------------------------------------------------ |
| `sns: topic name (URL host) is required`       | URL has no host and no ARN in path                     |
| `sns: topic name or ARN is required`           | URL has no host and path is not an ARN                 |
| `sns: failed to load AWS config: ...`          | AWS config could not be loaded from awscfg or defaults |
| `sns: failed to resolve topic ARN for "..."`   | `CreateTopic` API failed (no permissions, throttled)   |
| `sns: publish failed: ...`                     | `Publish` API call failed                              |
| `sns: batch publish failed: ...`               | `PublishBatch` API call failed                         |
| `sns: N messages failed in batch publish: ...` | Some entries in a batch were rejected by SNS           |
| `sns: receive is not supported...`             | `Receive` called — SNS is publish-only                 |
| `sns: receive batch is not supported...`       | `ReceiveBatch` called — SNS is publish-only            |
| `sns: add listener is not supported...`        | `AddListener` called — SNS is publish-only             |

### Unsupported Operations

SNS is a **publish-only** service from the messaging provider perspective. To receive messages published to SNS, subscribe an SQS queue to the SNS topic and use the [`sqs`](../sqs/) package:

```go
import (
    _ "oss.nandlabs.io/golly-aws/sns"   // publish
    _ "oss.nandlabs.io/golly-aws/sqs"   // receive
    "oss.nandlabs.io/golly/messaging"
)

// Publish to SNS topic
mgr.Send(snsURL, msg)

// Receive from SQS queue subscribed to the SNS topic
msg, err := mgr.Receive(sqsURL)
```

## API Reference

### Provider

Implements `messaging.Provider` (which extends `io.Closer`, `Producer`, and `Receiver`).

| Method                                         | Description                                                          |
| ---------------------------------------------- | -------------------------------------------------------------------- |
| `Id() string`                                  | Returns `"sns-provider"`                                             |
| `Schemes() []string`                           | Returns `["sns"]`                                                    |
| `Setup() error`                                | No-op initialization, always returns `nil`                           |
| `NewMessage(scheme, opts...) (Message, error)` | Creates a new `MessageSNS` wrapping a `BaseMessage` with a UUID      |
| `Send(u, msg, opts...) error`                  | Publishes a single message to a topic, phone number, or endpoint ARN |
| `SendBatch(u, msgs, opts...) error`            | Publishes messages in auto-chunked batches of 10                     |
| `Receive(u, opts...) (Message, error)`         | **Not supported** — returns error                                    |
| `ReceiveBatch(u, opts...) ([]Message, error)`  | **Not supported** — returns error                                    |
| `AddListener(u, fn, opts...) error`            | **Not supported** — returns error                                    |
| `Close() error`                                | No-op (no background goroutines to stop)                             |

### MessageSNS

Embeds `*messaging.BaseMessage` and provides SNS-specific methods.

| Method                             | Description                                                       |
| ---------------------------------- | ----------------------------------------------------------------- |
| `Rsvp(accept bool, opts...) error` | No-op — always returns `nil` (SNS has no message acknowledgement) |
| `SNSMessageId() string`            | Returns the SNS-assigned message ID (populated after `Send`)      |
| `Id() string`                      | Returns the message UUID (from BaseMessage)                       |

#### Inherited Body Methods

| Method                                          | Description                          |
| ----------------------------------------------- | ------------------------------------ |
| `SetBodyStr(s string) (int, error)`             | Set message body from a string       |
| `SetBodyBytes(b []byte) (int, error)`           | Set message body from bytes          |
| `SetFrom(r io.Reader) (int64, error)`           | Set message body from a reader       |
| `WriteJSON(v interface{}) error`                | Serialize value as JSON into body    |
| `WriteXML(v interface{}) error`                 | Serialize value as XML into body     |
| `WriteContent(v interface{}, ct string) error`  | Serialize with custom content type   |
| `ReadBody() io.Reader`                          | Get body as an `io.Reader`           |
| `ReadBytes() []byte`                            | Get body as a byte slice             |
| `ReadAsStr() string`                            | Get body as a string                 |
| `ReadJSON(out interface{}) error`               | Deserialize body from JSON           |
| `ReadXML(out interface{}) error`                | Deserialize body from XML            |
| `ReadContent(out interface{}, ct string) error` | Deserialize with custom content type |

## Prerequisites

### AWS Permissions

The IAM principal used must have the following SNS permissions:

| Action             | Required For                                      |
| ------------------ | ------------------------------------------------- |
| `sns:Publish`      | `Send`                                            |
| `sns:PublishBatch` | `SendBatch`                                       |
| `sns:CreateTopic`  | Topic ARN resolution (when using topic name URLs) |

> **Note:** If you use direct ARN URLs (`sns:///arn:aws:sns:...`), the `sns:CreateTopic` permission is not required.

### AWS Credentials

Credentials can be provided through any of the following (resolved by awscfg or the AWS SDK default chain):

- `awscfg.Config` with static credentials (`SetStaticCredentials`)
- `awscfg.Config` with a named profile (`SetProfile`)
- `awscfg.Config` with shared config/credentials files
- AWS environment variables (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`)
- IAM instance profile / ECS task role / EKS IRSA
- AWS SSO

## Contributing

We welcome contributions. If you find a bug or would like to request a new feature, please open an issue on [GitHub](https://github.com/nandlabs/golly-aws/issues).
