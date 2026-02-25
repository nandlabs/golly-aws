# sqs

AWS SQS implementation of the [golly messaging](https://pkg.go.dev/oss.nandlabs.io/golly/messaging) `Provider` interface.

---

- [Installation](#installation)
- [Features](#features)
- [Architecture](#architecture)
- [Auto-Registration](#auto-registration)
- [URL Format](#url-format)
- [Configuration](#configuration)
- [Queue URL Resolution](#queue-url-resolution)
- [Usage](#usage)
- [Message Headers & Attributes](#message-headers--attributes)
- [Options](#options)
- [FIFO Queue Support](#fifo-queue-support)
- [Error Handling](#error-handling)
- [Thread Safety & Graceful Shutdown](#thread-safety--graceful-shutdown)
- [API Reference](#api-reference)
- [Prerequisites](#prerequisites)
- [Contributing](#contributing)

---

## Installation

```bash
go get oss.nandlabs.io/golly-aws/sqs
```

## Features

- **Send** — send a single message to an SQS queue
- **SendBatch** — send up to N messages, automatically split into batches of 10 (SQS limit)
- **Receive** — receive a single message with configurable long-polling
- **ReceiveBatch** — receive up to 10 messages at once
- **AddListener** — continuously poll a queue in a background goroutine with automatic error backoff
- **Rsvp** — acknowledge (delete) or reject (change visibility to 0) messages
- **FIFO support** — message group ID and deduplication ID via options
- **Custom endpoint** — works with LocalStack, ElasticMQ, and other SQS-compatible services
- **Auto-registration** — blank import registers the SQS provider with the golly messaging manager
- **Config resolution** — leverages `awscfg` for per-queue or global AWS configuration
- **Thread-safe** — all listener management is protected with mutexes and atomic flags

## Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│  Application                                                     │
│                                                                  │
│  import _ "oss.nandlabs.io/golly-aws/sqs"                       │
│                                                                  │
│  mgr := messaging.GetManager()                                  │
│  mgr.Send(url, msg, opts...)                                    │
│  mgr.Receive(url, opts...)                                      │
│  mgr.AddListener(url, fn, opts...)                              │
└─────────────────────────┬────────────────────────────────────────┘
                          │
                          ▼
┌──────────────────────────────────────────────────────────────────┐
│  golly/messaging.Manager                                         │
│                                                                  │
│  Routes to provider by URL scheme ("sqs")                        │
│  Calls provider.Send / Receive / AddListener                    │
└─────────────────────────┬────────────────────────────────────────┘
                          │
                          ▼
┌──────────────────────────────────────────────────────────────────┐
│  sqs.Provider                                                    │
│                                                                  │
│  1. getSQSClient(u)       → awscfg.GetConfig(u, "sqs")         │
│  2. resolveQueueURL(c, u) → GetQueueUrl API or endpoint/acct/q │
│  3. SQS API call          → SendMessage / ReceiveMessage / etc  │
└─────────────────────────┬────────────────────────────────────────┘
                          │
                          ▼
┌──────────────────────────────────────────────────────────────────┐
│  awscfg.Manager                                                  │
│                                                                  │
│  Config resolution chain:                                        │
│  url.Host → url.Host+"/"+url.Path → fallback name ("sqs")      │
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

This means a **blank import** is all you need to make the SQS provider available:

```go
import _ "oss.nandlabs.io/golly-aws/sqs"
```

After this import, any call to `messaging.GetManager().Send(u, ...)` with a `sqs://` scheme URL will automatically route to this provider.

## URL Format

```
sqs://queue-name
sqs://queue-name/account-id
```

| Component | Maps To                                                          |
| --------- | ---------------------------------------------------------------- |
| Scheme    | `sqs` — used to route to this provider via the messaging manager |
| Host      | SQS queue name (e.g., `my-queue` or `my-queue.fifo`)             |
| Path      | Optional AWS account ID for cross-account queue access           |

**Examples:**

| URL                              | Queue Name    | Account ID       |
| -------------------------------- | ------------- | ---------------- |
| `sqs://my-queue`                 | `my-queue`    | _(caller's own)_ |
| `sqs://my-queue/123456789012`    | `my-queue`    | `123456789012`   |
| `sqs://orders.fifo`              | `orders.fifo` | _(caller's own)_ |
| `sqs://orders.fifo/123456789012` | `orders.fifo` | `123456789012`   |

## Configuration

Configuration is resolved via the [`awscfg`](../awscfg/) package. See the [awscfg README](../awscfg/README.md) for the full resolution mechanism.

### How Config Resolution Works

When `getSQSClient` is called with a URL like `sqs://my-queue`, the provider calls `awscfg.GetConfig(u, "sqs")` which tries the following resolution chain:

1. **`url.Host`** — look up `"my-queue"` in `awscfg.Manager`
2. **`url.Host + "/" + url.Path`** — look up `"my-queue/account-id"` (if path is present)
3. **Fallback name** — look up `"sqs"` in `awscfg.Manager`
4. **Default AWS config** — if no awscfg entry is found at all, loads the default AWS SDK config from environment/shared credentials

This lets you register per-queue configs, per-scheme defaults, or rely on the default AWS SDK resolution.

### Basic Setup

```go
import (
    "oss.nandlabs.io/golly-aws/awscfg"
    _ "oss.nandlabs.io/golly-aws/sqs"
    "oss.nandlabs.io/golly/messaging"
)

func main() {
    // Register a default config for all SQS operations
    cfg := awscfg.NewConfig("us-east-1")
    awscfg.Manager.Register("sqs", cfg)

    mgr := messaging.GetManager()
    // mgr.Send, mgr.Receive, etc.
}
```

### With LocalStack / Custom Endpoint

```go
cfg := awscfg.NewConfig("us-east-1")
cfg.SetEndpoint("http://localhost:4566")
cfg.SetStaticCredentials("test", "test", "")
awscfg.Manager.Register("sqs", cfg)
```

When a custom endpoint is configured, queue URL resolution changes: instead of calling the `GetQueueUrl` API, it constructs the URL directly as `endpoint/accountID/queueName`. See [Queue URL Resolution](#queue-url-resolution).

### Per-Queue Configuration

```go
// High-priority queue uses a specific profile
highPriorityCfg := awscfg.NewConfig("us-east-1")
highPriorityCfg.SetProfile("prod")
awscfg.Manager.Register("high-priority-queue", highPriorityCfg)

// Default for all other SQS queues
defaultCfg := awscfg.NewConfig("us-east-1")
awscfg.Manager.Register("sqs", defaultCfg)
```

**Resolution table for this setup:**

| URL                         | Resolved Config Key   |
| --------------------------- | --------------------- |
| `sqs://high-priority-queue` | `high-priority-queue` |
| `sqs://any-other-queue`     | `sqs` (fallback)      |

### Cross-Account Configuration

```go
// Queue in another account
crossAcctCfg := awscfg.NewConfig("us-west-2")
crossAcctCfg.SetProfile("cross-account-role")
awscfg.Manager.Register("shared-events", crossAcctCfg)

// Usage: the account ID in the URL is passed to GetQueueUrl API
u, _ := url.Parse("sqs://shared-events/987654321098")
mgr.Send(u, msg)
```

## Queue URL Resolution

The provider must convert a messaging URL (e.g., `sqs://my-queue`) to an actual SQS queue URL (e.g., `https://sqs.us-east-1.amazonaws.com/123456789012/my-queue`) before making API calls.

This is handled by `resolveQueueURL`:

### Real AWS (no custom endpoint)

Calls the [`GetQueueUrl`](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_GetQueueUrl.html) API:

```
Input:  sqs://my-queue
Action: GetQueueUrl(QueueName="my-queue")
Result: https://sqs.us-east-1.amazonaws.com/123456789012/my-queue
```

If the URL path contains an account ID, it is passed as `QueueOwnerAWSAccountId`:

```
Input:  sqs://shared-queue/987654321098
Action: GetQueueUrl(QueueName="shared-queue", QueueOwnerAWSAccountId="987654321098")
Result: https://sqs.us-east-1.amazonaws.com/987654321098/shared-queue
```

### Custom Endpoint (LocalStack, ElasticMQ)

When `awscfg.Config.Endpoint` is set, the URL is constructed directly without an API call:

```
Endpoint: http://localhost:4566
Input:    sqs://my-queue
Result:   http://localhost:4566/000000000000/my-queue

Input:    sqs://my-queue/123456789012
Result:   http://localhost:4566/123456789012/my-queue
```

The default account ID for custom endpoints is `000000000000`.

## Usage

### Sending a Message

```go
mgr := messaging.GetManager()
u, _ := url.Parse("sqs://my-queue")

msg, _ := mgr.NewMessage("sqs")
msg.SetBodyStr("Hello from golly SQS!")
msg.SetStrHeader("source", "my-service")

err := mgr.Send(u, msg)
if err != nil {
    log.Fatal(err)
}
```

### Sending a Batch

```go
u, _ := url.Parse("sqs://my-queue")

var msgs []messaging.Message
for i := 0; i < 25; i++ {
    msg, _ := mgr.NewMessage("sqs")
    msg.SetBodyStr(fmt.Sprintf("Message %d", i))
    msgs = append(msgs, msg)
}

// Automatically splits into batches of 10:
// batch 1: msgs[0:10], batch 2: msgs[10:20], batch 3: msgs[20:25]
err := mgr.SendBatch(u, msgs)
```

If any message in a batch fails, the error from the first failure is returned, including the SQS error message.

### Sending JSON / XML

```go
msg, _ := mgr.NewMessage("sqs")

// JSON
order := map[string]interface{}{"id": "123", "total": 99.99}
msg.WriteJSON(order)

// XML
msg.WriteXML(myXMLStruct)

// Custom content type
msg.WriteContent(data, "application/yaml")
```

### Receiving a Message

```go
u, _ := url.Parse("sqs://my-queue")

opts := messaging.NewOptionsBuilder().
    Add("WaitTimeSeconds", 10).
    Build()

msg, err := mgr.Receive(u, opts...)
if err != nil {
    log.Fatal(err)
}

// Read as string
fmt.Println("Body:", msg.ReadAsStr())

// Or deserialize JSON
var order Order
msg.ReadJSON(&order)

// Acknowledge the message (deletes from queue)
msg.Rsvp(true)
```

### Receiving a Batch

```go
u, _ := url.Parse("sqs://my-queue")

opts := messaging.NewOptionsBuilder().
    Add("BatchSize", 5).
    Add("WaitTimeSeconds", 10).
    Build()

msgs, err := mgr.ReceiveBatch(u, opts...)
if err != nil {
    log.Fatal(err)
}

for _, msg := range msgs {
    fmt.Println(msg.ReadAsStr())
    msg.Rsvp(true) // acknowledge each message
}
```

### Adding a Listener

```go
u, _ := url.Parse("sqs://my-queue")

opts := messaging.NewOptionsBuilder().
    Add("WaitTimeSeconds", 20).
    Add("Timeout", 300). // run for 5 minutes
    Build()

err := mgr.AddListener(u, func(msg messaging.Message) {
    fmt.Println("Received:", msg.ReadAsStr())
    msg.Rsvp(true)
}, opts...)
if err != nil {
    log.Fatal(err)
}

// Listener runs in a background goroutine.
// Multiple listeners can be active simultaneously.
// Call mgr.Close() to stop all listeners.
```

**Listener behavior:**

- Polls the queue continuously using long-polling with `WaitTimeSeconds`
- Receives up to 10 messages per poll
- Invokes the callback for each message sequentially
- On error: logs the error and waits 1 second before retrying (backoff)
- Stops when: `Close()` is called, context is cancelled, or `Timeout` expires
- If `Timeout` is set, uses `context.WithTimeout` to limit total listener duration

### Message Acknowledgement (Rsvp)

```go
// Accept: deletes the message from the queue
err := msg.Rsvp(true)

// Reject: changes visibility timeout to 0, making the message
// immediately available for reprocessing by another consumer
err := msg.Rsvp(false)
```

The `Rsvp` method maps to the following SQS API calls:

| `accept` | SQS API Called               | Effect                                           |
| -------- | ---------------------------- | ------------------------------------------------ |
| `true`   | `DeleteMessage`              | Message permanently removed from queue           |
| `false`  | `ChangeMessageVisibility(0)` | Message immediately visible for another consumer |

## Message Headers & Attributes

### Receiving

When messages are received from SQS, the provider maps **SQS message attributes** to golly **message headers**. Only string-type attributes are mapped:

```go
// SQS message attribute "source" = "my-service"
// becomes accessible as:
value, exists := msg.GetStrHeader("source")
```

All standard `MessageAttributes` with `StringValue` set are automatically converted to string headers on the received `MessageSQS`.

### Supported Header Types

The `MessageSQS` inherits all header methods from `BaseMessage`:

| Method             | Type      | Description           |
| ------------------ | --------- | --------------------- |
| `SetStrHeader`     | `string`  | Set a string header   |
| `SetIntHeader`     | `int`     | Set an integer header |
| `SetInt32Header`   | `int32`   | Set an int32 header   |
| `SetInt64Header`   | `int64`   | Set an int64 header   |
| `SetFloatHeader`   | `float32` | Set a float32 header  |
| `SetFloat64Header` | `float64` | Set a float64 header  |
| `SetBoolHeader`    | `bool`    | Set a boolean header  |
| `SetHeader`        | `[]byte`  | Set a raw byte header |
| `GetStrHeader`     | `string`  | Get a string header   |
| `GetIntHeader`     | `int`     | Get an integer header |
| `GetInt32Header`   | `int32`   | Get an int32 header   |
| `GetInt64Header`   | `int64`   | Get an int64 header   |
| `GetFloatHeader`   | `float32` | Get a float32 header  |
| `GetFloat64Header` | `float64` | Get a float64 header  |
| `GetBoolHeader`    | `bool`    | Get a boolean header  |
| `GetHeader`        | `[]byte`  | Get a raw byte header |

## Options

Options are passed via `messaging.Option` using the `OptionsBuilder`:

```go
opts := messaging.NewOptionsBuilder().
    Add("WaitTimeSeconds", 10).
    Add("VisibilityTimeout", 30).
    Build()
```

| Key                      | Type     | Default | Applies To                         | Description                                  |
| ------------------------ | -------- | ------- | ---------------------------------- | -------------------------------------------- |
| `WaitTimeSeconds`        | `int`    | `5`     | Receive, ReceiveBatch, AddListener | Long-poll wait time in seconds (0-20)        |
| `VisibilityTimeout`      | `int`    | —       | Receive, ReceiveBatch, AddListener | Visibility timeout for received messages (s) |
| `MaxMessages`            | `int`    | 1 / 10  | Receive, ReceiveBatch              | Max messages per receive (1-10)              |
| `BatchSize`              | `int`    | `10`    | ReceiveBatch                       | Batch size, auto-capped to 10                |
| `Timeout`                | `int`    | —       | AddListener                        | Total listener duration in seconds           |
| `MessageGroupId`         | `string` | —       | Send, SendBatch                    | Message group ID (required for FIFO queues)  |
| `MessageDeduplicationId` | `string` | —       | Send, SendBatch                    | Deduplication ID (FIFO queues)               |
| `DelaySeconds`           | `int`    | `0`     | Send, SendBatch                    | Per-message delay in seconds (0-900)         |

## FIFO Queue Support

For FIFO queues (queue names ending in `.fifo`), you **must** provide a `MessageGroupId`:

```go
u, _ := url.Parse("sqs://my-queue.fifo")

opts := messaging.NewOptionsBuilder().
    Add("MessageGroupId", "order-processing").
    Add("MessageDeduplicationId", "order-12345").
    Build()

msg, _ := mgr.NewMessage("sqs")
msg.SetBodyStr(`{"orderId": "12345"}`)

err := mgr.Send(u, msg, opts...)
```

**FIFO batch sends** apply the same `MessageGroupId` and `MessageDeduplicationId` to all messages in the batch. For per-message deduplication, send messages individually.

## Error Handling

All provider methods return descriptive errors prefixed with `sqs:`:

| Error                                       | When                                                           |
| ------------------------------------------- | -------------------------------------------------------------- |
| `sqs: queue name (URL host) is required`    | URL has no host (e.g., `sqs:///`)                              |
| `sqs: failed to load AWS config: ...`       | AWS config could not be loaded from awscfg or defaults         |
| `sqs: failed to get queue URL for "..."`    | `GetQueueUrl` API failed (queue doesn't exist, no permissions) |
| `sqs: send failed: ...`                     | `SendMessage` API call failed                                  |
| `sqs: batch send failed: ...`               | `SendMessageBatch` API call failed                             |
| `sqs: N messages failed in batch send: ...` | Some messages in a batch were rejected by SQS                  |
| `sqs: receive failed: ...`                  | `ReceiveMessage` API call failed                               |
| `sqs: receive batch failed: ...`            | `ReceiveMessage` API call failed (batch variant)               |
| `sqs: no messages available`                | No messages returned within the long-poll period               |
| `sqs: delete message failed: ...`           | `DeleteMessage` API call failed (Rsvp accept)                  |
| `sqs: change visibility failed: ...`        | `ChangeMessageVisibility` API call failed (Rsvp reject)        |

### Listener Error Handling

The `AddListener` goroutine handles errors internally:

1. If `ReceiveMessage` fails and the context is cancelled, the listener exits silently
2. If `ReceiveMessage` fails for other reasons, the error is logged via the `l3` logger, and the listener backs off for **1 second** before retrying
3. If the poll returns zero messages, the loop continues immediately (long-poll wait already provided by SQS)

## Thread Safety & Graceful Shutdown

### Thread Safety

The `Provider` struct is safe for concurrent use:

- **`closed`** flag uses `sync/atomic.Bool` — lock-free read/write from multiple goroutines
- **`stopFns`** slice (cancel functions for active listeners) is protected by `sync.Mutex`
- Each `Send`, `Receive`, `ReceiveBatch` call creates its own SQS client — no shared mutable state

### Graceful Shutdown

```go
// Stop all active listeners and release resources
err := mgr.Close()
```

Calling `Close()` on the provider (via the messaging manager):

1. Sets the `closed` atomic flag to `true`
2. Acquires the mutex and calls every registered `context.CancelFunc`
3. Clears the `stopFns` slice
4. All background listener goroutines observe the cancellation and exit

If a listener was started with a `Timeout`, it will also stop automatically when the timeout expires.

## API Reference

### Provider

Implements `messaging.Provider` (which extends `io.Closer`, `Producer`, and `Receiver`).

| Method                                         | Description                                                     |
| ---------------------------------------------- | --------------------------------------------------------------- |
| `Id() string`                                  | Returns `"sqs-provider"`                                        |
| `Schemes() []string`                           | Returns `["sqs"]`                                               |
| `Setup() error`                                | No-op initialization, always returns `nil`                      |
| `NewMessage(scheme, opts...) (Message, error)` | Creates a new `MessageSQS` wrapping a `BaseMessage` with a UUID |
| `Send(u, msg, opts...) error`                  | Sends a single message to the SQS queue                         |
| `SendBatch(u, msgs, opts...) error`            | Sends messages in auto-chunked batches of 10                    |
| `Receive(u, opts...) (Message, error)`         | Receives one message with long-polling (default 5s)             |
| `ReceiveBatch(u, opts...) ([]Message, error)`  | Receives up to 10 messages                                      |
| `AddListener(u, fn, opts...) error`            | Starts a background polling goroutine                           |
| `Close() error`                                | Stops all active listeners, cancels all contexts                |

### MessageSQS

Embeds `*messaging.BaseMessage` and adds SQS-specific acknowledgement.

| Method                             | Description                                                      |
| ---------------------------------- | ---------------------------------------------------------------- |
| `Rsvp(accept bool, opts...) error` | `true`: deletes message. `false`: resets visibility timeout to 0 |
| `Id() string`                      | Returns the message UUID (from BaseMessage)                      |

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

The IAM principal used must have the following SQS permissions:

| Action                        | Required For                             |
| ----------------------------- | ---------------------------------------- |
| `sqs:SendMessage`             | `Send`                                   |
| `sqs:SendMessageBatch`        | `SendBatch`                              |
| `sqs:ReceiveMessage`          | `Receive`, `ReceiveBatch`, `AddListener` |
| `sqs:DeleteMessage`           | `Rsvp(true)`                             |
| `sqs:ChangeMessageVisibility` | `Rsvp(false)`                            |
| `sqs:GetQueueUrl`             | All operations (real AWS only)           |

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
