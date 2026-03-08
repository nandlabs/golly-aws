# bedrock

AWS Bedrock implementation of the [golly genai](https://pkg.go.dev/oss.nandlabs.io/golly/genai) `Provider` interface.

> Uses the **Converse API** for a unified interface across all foundation models
> available on Amazon Bedrock — Anthropic Claude, Amazon Titan, Meta Llama,
> Mistral, Cohere, AI21, and more.

---

- [Installation](#installation)
- [Features](#features)
- [Architecture](#architecture)
- [Configuration](#configuration)
- [Usage](#usage)
- [Supported Content Types](#supported-content-types)
- [Options](#options)
- [Tool Use (Function Calling)](#tool-use-function-calling)
- [Streaming](#streaming)
- [Error Handling](#error-handling)
- [API Reference](#api-reference)
- [Prerequisites](#prerequisites)
- [Contributing](#contributing)

---

## Installation

```bash
go get oss.nandlabs.io/golly-aws/bedrock
```

## Features

- **Generate** — synchronous inference via the Bedrock Converse API
- **GenerateStream** — streaming inference via the Bedrock ConverseStream API
- **Multi-model** — works with any Bedrock model that supports the Converse API
- **Text, Images, and Documents** — send text, images (PNG/JPEG/GIF/WebP), and documents (PDF/CSV/DOC/DOCX/XLS/XLSX/HTML/TXT/MD)
- **Tool use** — full function calling / tool use support (send tool calls, receive tool results)
- **System prompts** — via options or system-role messages
- **Inference config** — max tokens, temperature, top-p, stop sequences
- **Token usage** — input, output, total, and cached token counts in response metadata
- **Custom endpoint** — works with LocalStack, Bedrock-compatible endpoints, and VPC endpoints
- **Config resolution** — leverages `awscfg` for AWS credential and region management

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  Application                                                 │
│                                                              │
│  genai.Provider interface                                    │
│  ┌────────────────────────────────────────────────────────┐  │
│  │  bedrock.BedrockProvider                               │  │
│  │                                                        │  │
│  │  ┌──────────────────────────────────────────────────┐  │  │
│  │  │  converseAPI (interface)                         │  │  │
│  │  │                                                  │  │  │
│  │  │  • Converse(ctx, input) → output                 │  │  │
│  │  │  • ConverseStream(ctx, input) → stream           │  │  │
│  │  └──────────────────────────────────────────────────┘  │  │
│  │                                                        │  │
│  │  ┌──────────────┐   ┌─────────────────────────────┐    │  │
│  │  │  utils.go     │  │  pkg.go                     │    │  │
│  │  │  • convert    │  │  • getBedrockClient()       │    │  │
│  │  │  • build      │  │  • awscfg integration       │    │  │
│  │  │  • map types  │  │  • logger                   │    │  │
│  │  └──────────────┘   └─────────────────────────────┘    │  │
│  └────────────────────────────────────────────────────────┘  │
│                              │                               │
│                              ▼                               │
│                     AWS Bedrock Runtime                      │
│                     (Converse API)                           │
└──────────────────────────────────────────────────────────────┘
```

## Configuration

### Using `ProviderConfig`

```go
provider, err := bedrock.NewBedrockProvider(&bedrock.ProviderConfig{
    // Use a named awscfg configuration
    CfgName: "my-bedrock-config",

    // Or provide an explicit awscfg.Config
    Config: &awscfg.Config{
        Region:   "us-east-1",
        Endpoint: "http://localhost:4566", // for LocalStack
    },

    // List of model IDs this provider instance supports
    Models: []string{
        "anthropic.claude-3-5-sonnet-20241022-v2:0",
        "amazon.titan-text-premier-v1:0",
    },

    // Optional custom description and version
    Description: "My Bedrock provider",
    Version:     "2.0.0",
})
```

### Configuration Resolution

The provider resolves AWS credentials and region in this order:

1. **Explicit `Config`** — uses the provided `awscfg.Config` directly
2. **Named config (`CfgName`)** — looks up a named config from `awscfg.Manager`
3. **Default** — loads the default AWS SDK config (env vars, `~/.aws/`, IMDS)

### Custom Endpoint

Set `Endpoint` on `awscfg.Config` to point the provider at a custom endpoint
(e.g., LocalStack, VPC endpoint, or a local mock):

```go
provider, err := bedrock.NewBedrockProvider(&bedrock.ProviderConfig{
    Config: &awscfg.Config{
        Region:   "us-east-1",
        Endpoint: "http://localhost:4566",
    },
})
```

## Usage

### Basic Text Generation

```go
package main

import (
    "context"
    "fmt"
    "log"

    "oss.nandlabs.io/golly-aws/bedrock"
    "oss.nandlabs.io/golly/genai"
)

func main() {
    provider, err := bedrock.NewBedrockProvider(&bedrock.ProviderConfig{
        Models: []string{"anthropic.claude-3-5-sonnet-20241022-v2:0"},
    })
    if err != nil {
        log.Fatal(err)
    }
    defer provider.Close()

    // Build the message
    msg := genai.NewTextMessage(genai.RoleUser, "What is Amazon Bedrock?")

    // Build options
    options := genai.NewOptionsBuilder().
        SetMaxTokens(1024).
        SetTemperature(0.7).
        Add(genai.OptionSystemInstructions, "You are a helpful assistant.").
        Build()

    // Generate
    resp, err := provider.Generate(
        context.Background(),
        "anthropic.claude-3-5-sonnet-20241022-v2:0",
        msg,
        options,
    )
    if err != nil {
        log.Fatal(err)
    }

    // Print the response
    for _, candidate := range resp.Candidates {
        for _, part := range candidate.Message.Parts {
            if part.Text != nil {
                fmt.Println(part.Text.Text)
            }
        }
    }

    fmt.Printf("Tokens: in=%d out=%d total=%d\n",
        resp.Meta.InputTokens, resp.Meta.OutputTokens, resp.Meta.TotalTokens)
}
```

### Streaming

```go
msg := genai.NewTextMessage(genai.RoleUser, "Tell me a story about Go.")

respChan, errChan := provider.GenerateStream(
    context.Background(),
    "anthropic.claude-3-5-sonnet-20241022-v2:0",
    msg,
    nil,
)

for resp := range respChan {
    for _, candidate := range resp.Candidates {
        if candidate.Message != nil {
            for _, part := range candidate.Message.Parts {
                if part.Text != nil {
                    fmt.Print(part.Text.Text)
                }
            }
        }
    }
    // Check token usage in metadata events
    if resp.Meta.TotalTokens > 0 {
        fmt.Printf("\nTokens: %d\n", resp.Meta.TotalTokens)
    }
}

if err := <-errChan; err != nil {
    log.Fatal(err)
}
```

## Supported Content Types

### Text

```go
msg := genai.NewTextMessage(genai.RoleUser, "Hello, world!")
```

### Images

Supported formats: PNG, JPEG, GIF, WebP

```go
imageData, _ := os.ReadFile("photo.png")
msg := &genai.Message{
    Role: genai.RoleUser,
    Parts: []genai.Part{
        {Text: &genai.TextPart{Text: "What's in this image?"}},
        {Name: "photo", MimeType: "image/png", Bin: &genai.BinPart{Data: imageData}},
    },
}
```

### Documents

Supported formats: PDF, CSV, DOC, DOCX, XLS, XLSX, HTML, TXT, Markdown

```go
pdfData, _ := os.ReadFile("report.pdf")
msg := &genai.Message{
    Role: genai.RoleUser,
    Parts: []genai.Part{
        {Text: &genai.TextPart{Text: "Summarize this document."}},
        {Name: "report", MimeType: "application/pdf", Bin: &genai.BinPart{Data: pdfData}},
    },
}
```

## Options

| Option                | Builder Method       | Description                          |
| --------------------- | -------------------- | ------------------------------------ |
| `max_tokens`          | `SetMaxTokens(n)`    | Maximum number of tokens to generate |
| `temperature`         | `SetTemperature(f)`  | Randomness (0.0–1.0)                 |
| `top_p`               | `SetTopP(f)`         | Nucleus sampling threshold           |
| `stop_words`          | `SetStopWords(s...)` | Stop sequences                       |
| `system_instructions` | `Add(key, val)`      | System prompt text                   |

```go
options := genai.NewOptionsBuilder().
    SetMaxTokens(2048).
    SetTemperature(0.5).
    SetTopP(0.9).
    SetStopWords("STOP", "END").
    Add(genai.OptionSystemInstructions, "You are a coding assistant.").
    Build()
```

## Tool Use (Function Calling)

### Sending a Tool Call Result

```go
// After receiving a tool call from the model, send the result back:
text := `{"temperature": "72°F", "condition": "sunny"}`
msg := &genai.Message{
    Role: genai.RoleUser,
    Parts: []genai.Part{
        {
            Name: "call_abc123", // the tool use ID from the model's response
            FuncResponse: &genai.FuncResponsePart{
                Text: &text,
            },
        },
    },
}
```

### Receiving a Tool Call

```go
resp, _ := provider.Generate(ctx, model, msg, opts)

for _, candidate := range resp.Candidates {
    if candidate.FinishReason == genai.FinishReasonToolCall {
        for _, part := range candidate.Message.Parts {
            if part.FuncCall != nil {
                fmt.Printf("Tool: %s, ID: %s, Args: %v\n",
                    part.FuncCall.FunctionName,
                    part.FuncCall.Id,
                    part.FuncCall.Arguments,
                )
            }
        }
    }
}
```

## Streaming

The `GenerateStream` method returns two channels:

| Channel               | Type             | Description                                   |
| --------------------- | ---------------- | --------------------------------------------- |
| `<-chan *GenResponse` | response channel | Text deltas, stop events, and metadata events |
| `<-chan error`        | error channel    | API or stream errors (at most one)            |

**Event types in the stream:**

| Event            | Content                                 |
| ---------------- | --------------------------------------- |
| Text delta       | Partial text in `Candidates[0].Message` |
| Message stop     | `FinishReason` set (end_turn, etc.)     |
| Metadata         | Token usage and latency in `Meta`       |
| Message start    | Skipped (nil response)                  |
| Block start/stop | Skipped (nil response)                  |

## Error Handling

The provider wraps all errors with descriptive context:

```go
resp, err := provider.Generate(ctx, model, msg, opts)
if err != nil {
    // Possible errors:
    // - "failed to build converse input: ..."
    // - "bedrock Converse API call failed: ..."
    // - "unsupported image format: ..."
    // - "unsupported document format: ..."
    log.Fatal(err)
}
```

## API Reference

### Types

| Type              | Description                                    |
| ----------------- | ---------------------------------------------- |
| `BedrockProvider` | Implements `genai.Provider` for AWS Bedrock    |
| `ProviderConfig`  | Configuration for creating a provider instance |

### Constants

| Constant           | Value     | Description        |
| ------------------ | --------- | ------------------ |
| `ProviderName`     | `bedrock` | Provider name      |
| `ProviderVersion`  | `1.0.0`   | Provider version   |
| `DefaultMaxTokens` | `4096`    | Default max tokens |

### Methods

| Method                                         | Description                       |
| ---------------------------------------------- | --------------------------------- |
| `NewBedrockProvider(config) (*Provider, err)`  | Create a new provider             |
| `Name() string`                                | Returns `"bedrock"`               |
| `Description() string`                         | Returns the provider description  |
| `Version() string`                             | Returns the provider version      |
| `Models() []string`                            | Returns configured model IDs      |
| `Generate(ctx, model, msg, opts) (*Resp, err)` | Synchronous generation            |
| `GenerateStream(ctx, model, msg, opts)`        | Streaming generation              |
| `Close() error`                                | No-op (no persistent connections) |

### Finish Reasons

| Bedrock StopReason | genai.FinishReason | Description                |
| ------------------ | ------------------ | -------------------------- |
| `end_turn`         | `EndTurn`          | Natural end of response    |
| `max_tokens`       | `Length`           | Token limit reached        |
| `stop_sequence`    | `Stop`             | Stop sequence matched      |
| `tool_use`         | `ToolCall`         | Model wants to call a tool |
| `content_filtered` | `ContentFilter`    | Content was filtered       |

## Prerequisites

- Go 1.22+
- AWS credentials configured (env vars, `~/.aws/credentials`, IAM role, etc.)
- Access to Amazon Bedrock models (request access in the AWS Console)
- Model access granted for the models you want to use

## Contributing

See [CONTRIBUTING.md](../CONTRIBUTING.md) for guidelines.
