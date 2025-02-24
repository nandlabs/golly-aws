# Messaging Implementation for SQS

This implementation provides you a set of standard functions to consume be the end user without worrying about any underlyig complexities.

---

- [Installation](#installation)
- [Features](#features)
- [Usage](#usage)
- [Examples](#examples)
- [Contributing](#contributing)

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

## URL Format to use

```bash
sqs://queue_name
```

## Usage

Setup the SQS library in order to start using it.
Under you main pacakge, you can add an init function or any method of your choice to initiate the library

The Priority of the Registered Provider is as follows

```bash
URL > HOST > Scheme("sqs") > default
```

```go
package main

import (
    "context"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/config"
)

func init() {
    conf, err := awssvc.CustomRegionConfig("ap-south-1")
    if err != nil {
        return
    }
    awssvc.Manager.Register("sqs", config)
}
```

## Examples

Here are some examples of how to use the SNS library:

1. Send a Message to SQS

    ```go
    package main

    import (
        "net/url"

        _ "oss.nandlabs.io/golly-aws/sqs"
    )

    func main() {
        manager := messaging.GetManager()
        u, err := url.Parse("sqs://queueName")
        if err != nil {
            fmt.Println(err)
        }
        message, err := manager.NewMessage(u.Scheme)
        if err != nil {
            fmt.Println(err)
        }
        message.SetBodyStr("hello sqs from golly")

        if err := manager.Send(u, message); err != nil {
            fmt.Println(err)
        }
    } 
    ```

2. Consume a Message from SQS

    ```go
    package main

    import (
        "net/url"

        _ "oss.nandlabs.io/golly-aws/sqs"
    )

    func main() {
        manager := messaging.getManager()
        u, err := url.Parse("sqs://queueName")
        if err != nil {
            fmt.Println(err)
        }
        msg, err := manager.Receive(u)
        if err != nil {
            // handle error
        }
        // handle received message (msg)
    }
    ```

3. Send multiple messages to SQS (Batch Message Processing)

    ```go
    package main

    import (
        "net/url"

        _ "oss.nandlabs.io/golly-aws/sqs"
    )

    func main() {
        manager := messaging.GetManager()
        u, err := url.Parse("sqs://queuename")
        if err != nil {
            // handle error
        }
        var messages []*messaging.Message
        msg1, err := manager.NewMessage(u.Scheme)
        if err != nil {
            // handle error
        }
        msg1.SetBodyStr("this is message1")
        messages = append(messages, msg1)
        msg2, err := manager.NewMessage(u.Scheme)
        if err != nil {
            // handle error
        }
        msg2.SetBodyStr("this is message2")
        messages = append(messages, msg2)
        if err := manager.SendBatch(u, messages); err != nil {
            // handle error
        }
    }
    ```

4. Consume multiple messages from SQS (Batch Message Processing)

    ```go
    package main

    import (
        "net/url"

        _ "oss.nandlabs.io/golly-aws/sqs"
    )

    func main() {
        manager := messaging.getManager()
        u, err := url.Parse("sqs://queueName")
        if err != nil {
            fmt.Println(err)
        }
        msgs, err := manager.ReceiveBatch(u)
        if err != nil {
            // handle error
        }
        for _, msg := range msgs {
            // handle received messages (msgs)
        }
    }
    ```

5. Add a listener to consume the messages

    ```go
    package main

    import (
        "net/url"

        _ "oss.nandlabs.io/golly-aws/sqs"
    )

    func main() {
        manager := messaging.getManager()
        u, err := url.Parse("sqs://queueName")
        if err != nil {
            fmt.Println(err)
        }
        handler := func(msg messaging.Message) {
            fmt.Printf("Received message ID: %s\nBody: %s\n", msg.ID, msg.Body)
            // Add your message processing logic here
        }

        err := manager.AddListener(u, handler, messaging.Option{Key: "MaxMessages", Value: int32(5)}, messaging.Option{Key: "WaitTime", Value: int32(10)})
        if err != nil {
            // handle error
        }
    }
    ```

## Contributing

We welcome contributions to the SQS library! If you find a bug, have a feature request, or want to contribute improvements, please create a pull request. For major changes, please open an issue first to discuss the changes you would like to make.
