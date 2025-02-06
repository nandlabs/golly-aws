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

1. Send a Message to SQS
2. Consume a Message from SQS
3. Send multiple messages to SQS (Batch Message Processing)
4. Consume multiple messages from SQS (Batch Message Processing)
5. Add a listener to consume the messages
