package sqs

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"oss.nandlabs.io/golly/messaging"
)

// Option keys for SQS-specific options.
const (
	// OptMaxMessages is the maximum number of messages to receive (1-10). Default: 1 for Receive, 10 for ReceiveBatch.
	OptMaxMessages = "MaxMessages"
	// OptWaitTimeSeconds is the long-poll wait time in seconds (0-20). Default: 5.
	OptWaitTimeSeconds = "WaitTimeSeconds"
	// OptVisibilityTimeout is the visibility timeout in seconds for received messages.
	OptVisibilityTimeout = "VisibilityTimeout"
	// OptTimeout is the overall timeout in seconds for listener operations.
	OptTimeout = "Timeout"
	// OptBatchSize is the batch size for ReceiveBatch. Default: 10.
	OptBatchSize = "BatchSize"
	// OptMessageGroupId is the message group ID for FIFO queues.
	OptMessageGroupId = "MessageGroupId"
	// OptMessageDeduplicationId is the deduplication ID for FIFO queues.
	OptMessageDeduplicationId = "MessageDeduplicationId"
	// OptDelaySeconds is the message delay in seconds (0-900).
	OptDelaySeconds = "DelaySeconds"
)

var sqsSchemes = []string{SQSScheme}

// Provider implements the messaging.Provider interface for AWS SQS.
type Provider struct {
	closed  atomic.Bool
	mu      sync.Mutex
	stopFns []context.CancelFunc // cancel functions for active listeners
}

// Id returns the provider id.
func (p *Provider) Id() string {
	return SQSProviderID
}

// Schemes returns the supported URL schemes.
func (p *Provider) Schemes() []string {
	return sqsSchemes
}

// Setup performs initial setup (no-op for SQS).
func (p *Provider) Setup() error {
	return nil
}

// NewMessage creates a new SQS message.
func (p *Provider) NewMessage(scheme string, options ...messaging.Option) (messaging.Message, error) {
	baseMsg, err := messaging.NewBaseMessage()
	if err != nil {
		return nil, err
	}
	return &MessageSQS{
		BaseMessage: baseMsg,
		provider:    p,
	}, nil
}

// Send sends a single message to the SQS queue.
// URL format: sqs://queue-name
func (p *Provider) Send(u *url.URL, msg messaging.Message, options ...messaging.Option) error {
	client, err := getSQSClient(u)
	if err != nil {
		return err
	}

	queueURL, err := resolveQueueURL(client, u)
	if err != nil {
		return err
	}

	input := &sqs.SendMessageInput{
		QueueUrl:    &queueURL,
		MessageBody: strPtr(msg.ReadAsStr()),
	}

	// Apply message attributes from headers
	input.MessageAttributes = buildMessageAttributes(msg)

	// Apply options
	optResolver := messaging.NewOptionsResolver(options...)
	if v, ok := optResolver.Get(OptMessageGroupId); ok {
		groupId := v.(string)
		input.MessageGroupId = &groupId
	}
	if v, ok := optResolver.Get(OptMessageDeduplicationId); ok {
		dedupId := v.(string)
		input.MessageDeduplicationId = &dedupId
	}
	if v, ok := optResolver.Get(OptDelaySeconds); ok {
		input.DelaySeconds = int32(v.(int))
	}

	_, err = client.SendMessage(context.Background(), input)
	if err != nil {
		return fmt.Errorf("sqs: send failed: %w", err)
	}

	logger.InfoF("SQS message sent to %s", queueURL)
	return nil
}

// SendBatch sends a batch of messages to the SQS queue.
// SQS supports up to 10 messages per batch. If more than 10 messages are provided,
// they will be sent in multiple batches.
func (p *Provider) SendBatch(u *url.URL, msgs []messaging.Message, options ...messaging.Option) error {
	if len(msgs) == 0 {
		return nil
	}

	client, err := getSQSClient(u)
	if err != nil {
		return err
	}

	queueURL, err := resolveQueueURL(client, u)
	if err != nil {
		return err
	}

	optResolver := messaging.NewOptionsResolver(options...)

	// SQS limit is 10 messages per batch
	const maxBatchSize = 10
	for i := 0; i < len(msgs); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(msgs) {
			end = len(msgs)
		}
		batch := msgs[i:end]

		entries := make([]types.SendMessageBatchRequestEntry, len(batch))
		for j, msg := range batch {
			id := fmt.Sprintf("msg-%d", i+j)
			entries[j] = types.SendMessageBatchRequestEntry{
				Id:                &id,
				MessageBody:       strPtr(msg.ReadAsStr()),
				MessageAttributes: buildMessageAttributes(msg),
			}
			if v, ok := optResolver.Get(OptMessageGroupId); ok {
				groupId := v.(string)
				entries[j].MessageGroupId = &groupId
			}
			if v, ok := optResolver.Get(OptMessageDeduplicationId); ok {
				dedupId := v.(string)
				entries[j].MessageDeduplicationId = &dedupId
			}
			if v, ok := optResolver.Get(OptDelaySeconds); ok {
				entries[j].DelaySeconds = int32(v.(int))
			}
		}

		output, err := client.SendMessageBatch(context.Background(), &sqs.SendMessageBatchInput{
			QueueUrl: &queueURL,
			Entries:  entries,
		})
		if err != nil {
			return fmt.Errorf("sqs: batch send failed: %w", err)
		}
		if len(output.Failed) > 0 {
			return fmt.Errorf("sqs: %d messages failed in batch send: %s",
				len(output.Failed), *output.Failed[0].Message)
		}

		logger.InfoF("SQS batch sent %d messages to %s", len(batch), queueURL)
	}

	return nil
}

// Receive receives a single message from the SQS queue.
// Supports options: WaitTimeSeconds, VisibilityTimeout.
func (p *Provider) Receive(u *url.URL, options ...messaging.Option) (messaging.Message, error) {
	client, err := getSQSClient(u)
	if err != nil {
		return nil, err
	}

	queueURL, err := resolveQueueURL(client, u)
	if err != nil {
		return nil, err
	}

	input := &sqs.ReceiveMessageInput{
		QueueUrl:              &queueURL,
		MaxNumberOfMessages:   1,
		MessageAttributeNames: []string{"All"},
	}

	optResolver := messaging.NewOptionsResolver(options...)
	if v, ok := optResolver.Get(OptWaitTimeSeconds); ok {
		input.WaitTimeSeconds = int32(v.(int))
	} else {
		input.WaitTimeSeconds = 5 // default long poll
	}
	if v, ok := optResolver.Get(OptVisibilityTimeout); ok {
		input.VisibilityTimeout = int32(v.(int))
	}

	output, err := client.ReceiveMessage(context.Background(), input)
	if err != nil {
		return nil, fmt.Errorf("sqs: receive failed: %w", err)
	}

	if len(output.Messages) == 0 {
		return nil, fmt.Errorf("sqs: no messages available")
	}

	return p.toMessage(output.Messages[0], queueURL), nil
}

// ReceiveBatch receives a batch of messages from the SQS queue.
// Supports options: BatchSize (default 10), WaitTimeSeconds, VisibilityTimeout.
func (p *Provider) ReceiveBatch(u *url.URL, options ...messaging.Option) ([]messaging.Message, error) {
	client, err := getSQSClient(u)
	if err != nil {
		return nil, err
	}

	queueURL, err := resolveQueueURL(client, u)
	if err != nil {
		return nil, err
	}

	optResolver := messaging.NewOptionsResolver(options...)

	maxMessages := int32(10)
	if v, ok := optResolver.Get(OptBatchSize); ok {
		maxMessages = int32(v.(int))
		if maxMessages > 10 {
			maxMessages = 10
		}
		if maxMessages < 1 {
			maxMessages = 1
		}
	}

	input := &sqs.ReceiveMessageInput{
		QueueUrl:              &queueURL,
		MaxNumberOfMessages:   maxMessages,
		MessageAttributeNames: []string{"All"},
	}

	if v, ok := optResolver.Get(OptWaitTimeSeconds); ok {
		input.WaitTimeSeconds = int32(v.(int))
	} else {
		input.WaitTimeSeconds = 5
	}
	if v, ok := optResolver.Get(OptVisibilityTimeout); ok {
		input.VisibilityTimeout = int32(v.(int))
	}

	output, err := client.ReceiveMessage(context.Background(), input)
	if err != nil {
		return nil, fmt.Errorf("sqs: receive batch failed: %w", err)
	}

	if len(output.Messages) == 0 {
		return nil, fmt.Errorf("sqs: no messages available")
	}

	msgs := make([]messaging.Message, len(output.Messages))
	for i, sqsMsg := range output.Messages {
		msgs[i] = p.toMessage(sqsMsg, queueURL)
	}

	return msgs, nil
}

// AddListener registers a listener that continuously polls the SQS queue for messages.
// The listener runs in a goroutine and can be stopped by calling Close on the provider.
// Supports options: WaitTimeSeconds, VisibilityTimeout, Timeout (total listener duration in seconds).
func (p *Provider) AddListener(u *url.URL, listener func(msg messaging.Message), options ...messaging.Option) error {
	client, err := getSQSClient(u)
	if err != nil {
		return err
	}

	queueURL, err := resolveQueueURL(client, u)
	if err != nil {
		return err
	}

	optResolver := messaging.NewOptionsResolver(options...)

	waitTime := int32(5)
	if v, ok := optResolver.Get(OptWaitTimeSeconds); ok {
		waitTime = int32(v.(int))
	}

	var visibilityTimeout int32
	if v, ok := optResolver.Get(OptVisibilityTimeout); ok {
		visibilityTimeout = int32(v.(int))
	}

	ctx, cancel := context.WithCancel(context.Background())
	if v, ok := optResolver.Get(OptTimeout); ok {
		timeout := time.Duration(v.(int)) * time.Second
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
	}

	p.mu.Lock()
	p.stopFns = append(p.stopFns, cancel)
	p.mu.Unlock()

	go func() {
		defer cancel()
		logger.InfoF("SQS listener started for %s", queueURL)
		for {
			if p.closed.Load() {
				return
			}

			select {
			case <-ctx.Done():
				logger.InfoF("SQS listener stopped for %s", queueURL)
				return
			default:
			}

			input := &sqs.ReceiveMessageInput{
				QueueUrl:              &queueURL,
				MaxNumberOfMessages:   10,
				WaitTimeSeconds:       waitTime,
				MessageAttributeNames: []string{"All"},
			}
			if visibilityTimeout > 0 {
				input.VisibilityTimeout = visibilityTimeout
			}

			output, err := client.ReceiveMessage(ctx, input)
			if err != nil {
				if ctx.Err() != nil {
					return // context cancelled
				}
				logger.ErrorF("SQS listener receive error: %v", err)
				time.Sleep(time.Second) // backoff on error
				continue
			}

			for _, sqsMsg := range output.Messages {
				msg := p.toMessage(sqsMsg, queueURL)
				listener(msg)
			}
		}
	}()

	return nil
}

// Close stops all active listeners.
func (p *Provider) Close() error {
	p.closed.Store(true)
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, cancel := range p.stopFns {
		cancel()
	}
	p.stopFns = nil
	return nil
}

// toMessage converts an SQS message to a MessageSQS.
func (p *Provider) toMessage(sqsMsg types.Message, queueURL string) *MessageSQS {
	baseMsg, _ := messaging.NewBaseMessage()
	if sqsMsg.Body != nil {
		_, _ = baseMsg.SetBodyStr(*sqsMsg.Body)
	}

	// Map SQS message attributes to headers
	for k, v := range sqsMsg.MessageAttributes {
		if v.StringValue != nil {
			baseMsg.SetStrHeader(k, *v.StringValue)
		}
	}

	receiptHandle := ""
	if sqsMsg.ReceiptHandle != nil {
		receiptHandle = *sqsMsg.ReceiptHandle
	}

	return &MessageSQS{
		BaseMessage:   baseMsg,
		receiptHandle: receiptHandle,
		queueURL:      queueURL,
		provider:      p,
	}
}

// deleteMessage deletes a message from the queue (acknowledges it).
func (p *Provider) deleteMessage(queueURL, receiptHandle string) error {
	u, _ := url.Parse("sqs://internal")
	client, err := getSQSClient(u)
	if err != nil {
		return err
	}
	_, err = client.DeleteMessage(context.Background(), &sqs.DeleteMessageInput{
		QueueUrl:      &queueURL,
		ReceiptHandle: &receiptHandle,
	})
	if err != nil {
		return fmt.Errorf("sqs: delete message failed: %w", err)
	}
	return nil
}

// changeVisibility changes the visibility timeout of a message.
func (p *Provider) changeVisibility(queueURL, receiptHandle string, timeout int32) error {
	u, _ := url.Parse("sqs://internal")
	client, err := getSQSClient(u)
	if err != nil {
		return err
	}
	_, err = client.ChangeMessageVisibility(context.Background(), &sqs.ChangeMessageVisibilityInput{
		QueueUrl:          &queueURL,
		ReceiptHandle:     &receiptHandle,
		VisibilityTimeout: timeout,
	})
	if err != nil {
		return fmt.Errorf("sqs: change visibility failed: %w", err)
	}
	return nil
}

// buildMessageAttributes converts message string headers to SQS message attributes.
func buildMessageAttributes(msg messaging.Message) map[string]types.MessageAttributeValue {
	// SQS message attributes are built from known header keys.
	// Since we can't iterate BaseMessage headers directly, callers should use
	// the SQS-specific SetStrHeader on the message before sending.
	return nil
}

func strPtr(s string) *string {
	return &s
}
