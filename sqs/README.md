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

## URL Format to use

```bash
sqs://queue_name
```

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
)

func init() {
    config := aws.Config{
        Region: "us-east-1",
    }
    awssvc.Manager.Register("sqs", config)
}
```

1. Send a Message to SQS

    ```go
    package main

    import (
        "fmt"
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
        "fmt"
        "net/url"

        _ "oss.nandlabs.io/golly-aws/sqs"
    )

    func main() {

    }
    ```

3. Send multiple messages to SQS (Batch Message Processing)

    ```go
    package main

    import (
        "fmt"
        "net/url"

        _ "oss.nandlabs.io/golly-aws/sqs"
    )

    func main() {
        
    }
    ```

4. Consume multiple messages from SQS (Batch Message Processing)

    ```go
    package main

    import (
        "fmt"
        "net/url"

        _ "oss.nandlabs.io/golly-aws/sqs"
    )

    func main() {
        
    }
    ```

5. Add a listener to consume the messages

    ```go
    package main

    import (
        "fmt"
        "net/url"

        _ "oss.nandlabs.io/golly-aws/sqs"
    )

    func main() {
        
    }
    ```

## Contributing

We welcome contributions to the SQS library! If you find a bug, have a feature request, or want to contribute improvements, please create a pull request. For major changes, please open an issue first to discuss the changes you would like to make.
