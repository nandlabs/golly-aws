# Messaging Implementation for SQS

This implementation provides you a set of standard functions to consume be the end user without worrying about any underlyig complexities.

---

- [Installation](#installation)
- [Features](#features)
- [Usage](#usage)

---

## Installation

```bash
go get oss.nandlabs.io/golly-aws/sqs
```

## Features

A number of features are provided out of the box

- Ability to send a message to SQS
- Ability to send multiple messages to SQS
- Ability to consume messages from SQS
- Ability to consume multiple messages from SQS

## Usage

Setup the SQS library in order to start using it.
Under you main pacakge, you can add an init function or any method of your choice to initiate the library

```go
package main

import (
    "context"
    "fmt"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/config"
    "oss.nandlabs.io/golly-aws/sqs"
)

type SqsSessionProvider struct {
    region string
}

func (sqsSessionProvider *SqsSessionProvider) Get() (*aws.Config, error) {
    sess, err := config.LoadDefaultConfig(context.Background(), config.WithRegion(sqsSessionProvider.region))
    return &sess, err
}

func init() {
    fmt.Println("testing sqs")

    pvd := &SqsSessionProvider{
        region: "us-east-1",
    }
    sqs.AddSessionProvider(pvd.region, pvd)
}
```

1. Send a Message to SQS

    ```go
    package main

    import (
        "fmt"
        "net/url"

        "oss.nandlabs.io/golly-aws/sqs"
    )

    func main() {
        sqsProvider := &sqs.ProviderSQS{}
        url := &url.URL{Scheme: "sqs", Host: "example.com"}
        message := sqs.NewSQSMessage()
        // message.SetBodyStr("hello from golly")

        if err := sqsProvider.Send(url, message); err != nil {
            fmt.Println(err)
        }
    } 
    ```

2. Consume a Message from SQS
3. Send multiple messages to SQS (Batch Message Processing)
4. Consume multiple messages from SQS (Batch Message Processing)
5. Add a listener to consume the messages
