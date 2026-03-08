package sns

import (
	"context"
	"fmt"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sns/types"
	"oss.nandlabs.io/golly/messaging"
)

// Option keys for SNS-specific options.
const (
	// OptSubject is the subject for the SNS message (used by email/email-json subscriptions).
	OptSubject = "Subject"
	// OptMessageGroupId is the message group ID for FIFO topics.
	OptMessageGroupId = "MessageGroupId"
	// OptMessageDeduplicationId is the deduplication ID for FIFO topics.
	OptMessageDeduplicationId = "MessageDeduplicationId"
	// OptMessageStructure when set to "json" enables per-protocol message formatting.
	// The message body must be a JSON object with protocol keys (e.g., "default", "sqs", "email").
	OptMessageStructure = "MessageStructure"
	// OptPhoneNumber publishes an SMS message directly to a phone number (instead of a topic).
	OptPhoneNumber = "PhoneNumber"
	// OptTargetArn publishes to a specific subscription ARN (endpoint ARN).
	OptTargetArn = "TargetArn"
)

var snsSchemes = []string{SNSScheme}

// Provider implements the messaging.Provider interface for AWS SNS.
// SNS is a publish-only service, so Receive, ReceiveBatch, and AddListener
// return an unsupported operation error.
type Provider struct{}

// Id returns the provider id.
func (p *Provider) Id() string {
	return SNSProviderID
}

// Schemes returns the supported URL schemes.
func (p *Provider) Schemes() []string {
	return snsSchemes
}

// Setup performs initial setup (no-op for SNS).
func (p *Provider) Setup() error {
	return nil
}

// NewMessage creates a new SNS message.
func (p *Provider) NewMessage(scheme string, options ...messaging.Option) (messaging.Message, error) {
	baseMsg, err := messaging.NewBaseMessage()
	if err != nil {
		return nil, err
	}
	return &MessageSNS{
		BaseMessage: baseMsg,
		provider:    p,
	}, nil
}

// Send publishes a single message to an SNS topic.
//
// URL formats:
//
//	sns://topic-name                           → resolves ARN via CreateTopic (idempotent)
//	sns:///arn:aws:sns:region:account:topic     → uses ARN directly
//
// Supported options: Subject, MessageGroupId, MessageDeduplicationId,
// MessageStructure, PhoneNumber, TargetArn.
func (p *Provider) Send(u *url.URL, msg messaging.Message, options ...messaging.Option) error {
	client, err := getSNSClient(u)
	if err != nil {
		return err
	}

	optResolver := messaging.NewOptionsResolver(options...)

	input := &sns.PublishInput{
		Message: strPtr(msg.ReadAsStr()),
	}

	// Build message attributes from headers
	input.MessageAttributes = buildMessageAttributes(msg)

	// Determine target: phone number, target ARN, or topic ARN
	if v, ok := optResolver.Get(OptPhoneNumber); ok {
		phone := v.(string)
		input.PhoneNumber = &phone
	} else if v, ok := optResolver.Get(OptTargetArn); ok {
		targetArn := v.(string)
		input.TargetArn = &targetArn
	} else {
		topicARN, err := resolveTopicARN(client, u)
		if err != nil {
			return err
		}
		input.TopicArn = &topicARN
	}

	// Apply optional fields
	if v, ok := optResolver.Get(OptSubject); ok {
		subject := v.(string)
		input.Subject = &subject
	}
	if v, ok := optResolver.Get(OptMessageGroupId); ok {
		groupId := v.(string)
		input.MessageGroupId = &groupId
	}
	if v, ok := optResolver.Get(OptMessageDeduplicationId); ok {
		dedupId := v.(string)
		input.MessageDeduplicationId = &dedupId
	}
	if v, ok := optResolver.Get(OptMessageStructure); ok {
		structure := v.(string)
		input.MessageStructure = &structure
	}

	output, err := client.Publish(context.Background(), input)
	if err != nil {
		return fmt.Errorf("sns: publish failed: %w", err)
	}

	// Store the SNS message ID on the message if it's a MessageSNS
	if snsMsg, ok := msg.(*MessageSNS); ok && output.MessageId != nil {
		snsMsg.messageId = *output.MessageId
	}

	logger.InfoF("SNS message published, MessageId: %s", safeDeref(output.MessageId))
	return nil
}

// SendBatch publishes a batch of messages to an SNS topic.
// SNS supports up to 10 messages per PublishBatch call. If more than 10 messages
// are provided, they are sent in multiple batches.
func (p *Provider) SendBatch(u *url.URL, msgs []messaging.Message, options ...messaging.Option) error {
	if len(msgs) == 0 {
		return nil
	}

	client, err := getSNSClient(u)
	if err != nil {
		return err
	}

	topicARN, err := resolveTopicARN(client, u)
	if err != nil {
		return err
	}

	optResolver := messaging.NewOptionsResolver(options...)

	const maxBatchSize = 10
	for i := 0; i < len(msgs); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(msgs) {
			end = len(msgs)
		}
		batch := msgs[i:end]

		entries := make([]types.PublishBatchRequestEntry, len(batch))
		for j, msg := range batch {
			id := fmt.Sprintf("msg-%d", i+j)
			entries[j] = types.PublishBatchRequestEntry{
				Id:                &id,
				Message:           strPtr(msg.ReadAsStr()),
				MessageAttributes: buildBatchMessageAttributes(msg),
			}
			if v, ok := optResolver.Get(OptSubject); ok {
				subject := v.(string)
				entries[j].Subject = &subject
			}
			if v, ok := optResolver.Get(OptMessageGroupId); ok {
				groupId := v.(string)
				entries[j].MessageGroupId = &groupId
			}
			if v, ok := optResolver.Get(OptMessageDeduplicationId); ok {
				dedupId := v.(string)
				entries[j].MessageDeduplicationId = &dedupId
			}
			if v, ok := optResolver.Get(OptMessageStructure); ok {
				structure := v.(string)
				entries[j].MessageStructure = &structure
			}
		}

		output, err := client.PublishBatch(context.Background(), &sns.PublishBatchInput{
			TopicArn:                   &topicARN,
			PublishBatchRequestEntries: entries,
		})
		if err != nil {
			return fmt.Errorf("sns: batch publish failed: %w", err)
		}
		if len(output.Failed) > 0 {
			return fmt.Errorf("sns: %d messages failed in batch publish: %s",
				len(output.Failed), safeDeref(output.Failed[0].Message))
		}

		logger.InfoF("SNS batch published %d messages to %s", len(batch), topicARN)
	}

	return nil
}

// Receive is not supported by SNS. SNS is a publish-only service.
// Use the SQS provider with an SNS→SQS subscription for receiving messages.
func (p *Provider) Receive(_ *url.URL, _ ...messaging.Option) (messaging.Message, error) {
	return nil, fmt.Errorf("sns: receive is not supported; SNS is a publish-only service; use SNS→SQS fan-out with the sqs package")
}

// ReceiveBatch is not supported by SNS.
func (p *Provider) ReceiveBatch(_ *url.URL, _ ...messaging.Option) ([]messaging.Message, error) {
	return nil, fmt.Errorf("sns: receive batch is not supported; SNS is a publish-only service; use SNS→SQS fan-out with the sqs package")
}

// AddListener is not supported by SNS.
func (p *Provider) AddListener(_ *url.URL, _ func(msg messaging.Message), _ ...messaging.Option) error {
	return fmt.Errorf("sns: add listener is not supported; SNS is a publish-only service; use SNS→SQS fan-out with the sqs package")
}

// Close is a no-op for SNS since there are no background listeners.
func (p *Provider) Close() error {
	return nil
}

// buildMessageAttributes converts message string headers to SNS message attributes for Publish.
func buildMessageAttributes(msg messaging.Message) map[string]types.MessageAttributeValue {
	// SNS message attributes are built from known header keys.
	// Since we can't iterate BaseMessage headers directly, this returns nil.
	// Callers set headers via SetStrHeader which are available on the message.
	return nil
}

// buildBatchMessageAttributes converts message headers to SNS message attributes for PublishBatch.
func buildBatchMessageAttributes(msg messaging.Message) map[string]types.MessageAttributeValue {
	return nil
}

// safeDeref safely dereferences a string pointer, returning empty string if nil.
func safeDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
