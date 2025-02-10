package sqs

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"oss.nandlabs.io/golly/messaging"
	"oss.nandlabs.io/golly/uuid"
)

const (
	SchemeSqs   = "sqs"
	SQSProvider = "sqs-provider"
)

type ReceiveOptions struct {
	MaxMessages int32
	WaitTime    int32
}

var sqsSchemes = []string{SchemeSqs}

type ProviderSQS struct {
}

func (sqsp *ProviderSQS) Schemes() (schemes []string) {
	schemes = sqsSchemes
	return
}

func (sqsp *ProviderSQS) Setup() (err error) {

	return nil
}

func (sqsp *ProviderSQS) NewMessage(scheme string, options ...messaging.Option) (msg messaging.Message, err error) {
	baseMsg, err := messaging.NewBaseMessage()
	if err == nil {
		msg = &MessageSQS{
			BaseMessage: baseMsg,
		}
	}
	return
}

func (sqsp *ProviderSQS) Send(url *url.URL, msg messaging.Message, options ...messaging.Option) (err error) {
	client, err := GetClient(url)
	if err != nil {
		return
	}

	input := &sqs.SendMessageInput{
		MessageBody: aws.String(msg.ReadAsStr()),
		QueueUrl:    aws.String(url.String()),
	}
	_, err = client.SendMessage(context.Background(), input)
	return
}

func (sqsp *ProviderSQS) SendBatch(url *url.URL, msgs []messaging.Message, options ...messaging.Option) (err error) {
	client, err := GetClient(url)
	if err != nil {
		return
	}

	var publishBatchEntries []types.SendMessageBatchRequestEntry
	for _, msg := range msgs {
		itemId, err := uuid.V4()
		if err != nil {
			return err
		}
		input := types.SendMessageBatchRequestEntry{
			Id:          aws.String(itemId.String()),
			MessageBody: aws.String(msg.ReadAsStr()),
		}
		publishBatchEntries = append(publishBatchEntries, input)
	}

	publishBatchInput := &sqs.SendMessageBatchInput{
		Entries:  publishBatchEntries,
		QueueUrl: aws.String(url.String()),
	}
	_, err = client.SendMessageBatch(context.Background(), publishBatchInput)
	return
}

func (sqsp *ProviderSQS) Receive(source *url.URL, options ...messaging.Option) (msg messaging.Message, err error) {
	client, err := GetClient(source)
	if err != nil {
		return
	}

	input := &sqs.ReceiveMessageInput{
		MessageAttributeNames: []string{
			string(types.QueueAttributeNameAll),
		},
		QueueUrl:            aws.String(source.String()),
		MaxNumberOfMessages: 1,
	}
	msgResult, err := client.ReceiveMessage(context.TODO(), input)
	if err != nil {
		return
	}

	baseMsg := &messaging.BaseMessage{}
	baseMsg.SetBodyStr(*msgResult.Messages[0].Body)

	msg = &MessageSQS{
		baseMsg,
	}
	return
}

func (sqsp *ProviderSQS) ReceiveBatch(source *url.URL, options ...messaging.Option) (msgs []messaging.Message, err error) {
	client, err := GetClient(source)
	if err != nil {
		return
	}

	receiveOptions := ReceiveOptions{
		MaxMessages: 10, // Default max messages
		WaitTime:    0,  // Default wait time
	}

	for _, opt := range options {
		switch opt.Key {
		case "MaxMessages":
			if v, ok := opt.Value.(int32); ok {
				receiveOptions.MaxMessages = v
			}
		case "WaitTime":
			if v, ok := opt.Value.(int32); ok {
				receiveOptions.WaitTime = v
			}
		}
	}

	// Receive Messages
	input := &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(source.String()),
		MaxNumberOfMessages: receiveOptions.MaxMessages,
		WaitTimeSeconds:     receiveOptions.WaitTime,
	}

	output, err := client.ReceiveMessage(context.TODO(), input)
	if err != nil {
		return nil, fmt.Errorf("failed to receive messages: %w", err)
	}

	// Map SQS messages to messaging.Message
	for _, m := range output.Messages {
		msg, _ := sqsp.NewMessage(source.Scheme)
		msg.SetBodyStr(*m.Body)
		// m.ReceiptHandle
		msg.SetHeader("Receipt", []byte(*m.ReceiptHandle))
		msgs = append(msgs, msg)
	}

	return msgs, nil
}

func (sqsp *ProviderSQS) AddListener(url *url.URL, listener func(msg messaging.Message), options ...messaging.Option) (err error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			msgs, err := sqsp.ReceiveBatch(url, options...)
			if err != nil {
				return fmt.Errorf("failed to receive messages: %w", err)
			}

			for _, msg := range msgs {
				listener(msg)
			}

			// Avoid rapid polling if not messages are received
			if len(msgs) == 0 {
				time.Sleep(1 * time.Second)
			}

		}
	}
}

func (sqsp *ProviderSQS) Close() (err error) {
	// TODO should be used to close the listener
	return
}

func (sqsp *ProviderSQS) Id() string {
	return SQSProvider
}
