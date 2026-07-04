package sns

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

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
//
// Provider additionally implements the following v1.7.0 messaging
// capabilities:
//
//   - messaging.ProducerCtx — SendCtx / SendBatchCtx propagate the
//     caller's context into the AWS SDK v2 client for genuine
//     cancellation and deadline support.
//   - messaging.ObservableProvider — SetObserver installs a metrics /
//     tracing hook fired around each Publish / PublishBatch. Only
//     OnSend is fired (SNS is fire-and-forget; there is no
//     Receive / Ack / Nack path on the publisher side).
//
// Keyed messages (messaging.Keyed): when the resolved topic ARN ends
// in ".fifo", the message's RoutingKey() is mapped to
// PublishInput.MessageGroupId ("default" if empty). On standard
// (non-FIFO) topics the routing key is ignored silently.
//
// The ReceiverCtx capability is intentionally not implemented — SNS
// has no publisher-side receive semantics; use the SNS→SQS fan-out
// pattern with the sqs package for context-aware receives.
type Provider struct {
	// observer is loaded atomically at each hook site so SetObserver
	// is safe to call concurrently with in-flight Publish calls.
	observer atomic.Value // holds messaging.Observer (may be nil)
}

// SetObserver installs (or clears, with nil) the metrics / tracing
// hook observer. Safe to call concurrently at any point in the
// provider's lifecycle. Implements messaging.ObservableProvider.
func (p *Provider) SetObserver(obs messaging.Observer) {
	if obs == nil {
		// atomic.Value cannot store a typed-nil directly; use a
		// sentinel empty-interface wrapper so hook sites can uniformly
		// load-and-check.
		p.observer.Store((*observerHolder)(nil))
		return
	}
	p.observer.Store(&observerHolder{obs: obs})
}

// loadObserver returns the currently registered observer or nil.
func (p *Provider) loadObserver() messaging.Observer {
	v := p.observer.Load()
	if v == nil {
		return nil
	}
	h, ok := v.(*observerHolder)
	if !ok || h == nil {
		return nil
	}
	return h.obs
}

// observerHolder wraps an Observer for atomic.Value (which requires a
// stable concrete type across stores).
type observerHolder struct {
	obs messaging.Observer
}

// fireOnSend safely invokes the observer's OnSend hook if one is
// registered. Callers pass the start time; latency is computed here.
func (p *Provider) fireOnSend(u *url.URL, msg messaging.Message, err error, start time.Time) {
	if obs := p.loadObserver(); obs != nil {
		obs.OnSend(u, msg, err, time.Since(start))
	}
}

// Compile-time interface assertions for v1.7.0 capabilities.
var (
	_ messaging.Producer           = (*Provider)(nil)
	_ messaging.ProducerCtx        = (*Provider)(nil)
	_ messaging.ObservableProvider = (*Provider)(nil)
)

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
//
// Send delegates to SendCtx with a background context. Callers that
// need cancellation / deadline support should call SendCtx directly.
func (p *Provider) Send(u *url.URL, msg messaging.Message, options ...messaging.Option) error {
	return p.SendCtx(context.Background(), u, msg, options...)
}

// SendCtx is the context-aware variant of Send. It propagates ctx into
// the AWS SDK v2 sns.Publish call for genuine cancellation and
// deadline support. Implements messaging.ProducerCtx.
//
// FIFO routing: when the resolved topic ARN ends in ".fifo" and msg
// satisfies messaging.Keyed, the routing key is passed as
// MessageGroupId ("default" when the key is empty). On non-FIFO topics
// any routing key is ignored silently. An explicit MessageGroupId
// option always wins over the Keyed-derived value.
func (p *Provider) SendCtx(ctx context.Context, u *url.URL, msg messaging.Message, options ...messaging.Option) error {
	start := time.Now()
	err := p.sendCtx(ctx, u, msg, options...)
	p.fireOnSend(u, msg, err, start)
	return err
}

// sendCtx implements the Publish flow; SendCtx wraps it with observer
// hooks so early returns are still observed.
func (p *Provider) sendCtx(ctx context.Context, u *url.URL, msg messaging.Message, options ...messaging.Option) error {
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

	// Determine target: phone number, target ARN, or topic ARN. Only
	// the topic ARN path can be FIFO, so Keyed → MessageGroupId
	// mapping only applies there.
	var topicARN string
	usingTopic := false
	if v, ok := optResolver.Get(OptPhoneNumber); ok {
		phone := v.(string)
		input.PhoneNumber = &phone
	} else if v, ok := optResolver.Get(OptTargetArn); ok {
		targetArn := v.(string)
		input.TargetArn = &targetArn
	} else {
		topicARN, err = resolveTopicARN(client, u)
		if err != nil {
			return err
		}
		input.TopicArn = &topicARN
		usingTopic = true
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

	// Keyed → FIFO MessageGroupId. Explicit option wins; otherwise map
	// the routing key (falling back to "default" when it is empty).
	if usingTopic && input.MessageGroupId == nil && isFIFOTopic(topicARN) {
		groupId := messaging.RoutingKeyOf(msg)
		if groupId == "" {
			groupId = "default"
		}
		input.MessageGroupId = &groupId
	}

	output, err := client.Publish(ctx, input)
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

// isFIFOTopic reports whether the given topic ARN is a FIFO topic.
// AWS suffixes FIFO topic names with ".fifo".
func isFIFOTopic(topicARN string) bool {
	return strings.HasSuffix(topicARN, ".fifo")
}

// SendBatch publishes a batch of messages to an SNS topic.
// SNS supports up to 10 messages per PublishBatch call. If more than 10 messages
// are provided, they are sent in multiple batches.
//
// SendBatch delegates to SendBatchCtx with a background context.
func (p *Provider) SendBatch(u *url.URL, msgs []messaging.Message, options ...messaging.Option) error {
	return p.SendBatchCtx(context.Background(), u, msgs, options...)
}

// SendBatchCtx is the context-aware variant of SendBatch. It propagates
// ctx into each underlying sns.PublishBatch call for genuine
// cancellation and deadline support. Implements messaging.ProducerCtx.
//
// FIFO routing: mirrors SendCtx — on FIFO topics each batch entry's
// MessageGroupId is taken from the per-message routing key (defaulting
// to "default" when empty). An explicit MessageGroupId option applies
// uniformly to every entry and wins over the Keyed value.
//
// The observer's OnSend fires once per underlying PublishBatch call
// (i.e. per chunk of up to 10 messages) with the batch's first
// message so callers can attribute the latency to the URL.
func (p *Provider) SendBatchCtx(ctx context.Context, u *url.URL, msgs []messaging.Message, options ...messaging.Option) error {
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
	fifo := isFIFOTopic(topicARN)

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
			// Keyed → FIFO MessageGroupId (per entry). Explicit option
			// above wins; skip if the field is already populated.
			if fifo && entries[j].MessageGroupId == nil {
				groupId := messaging.RoutingKeyOf(msg)
				if groupId == "" {
					groupId = "default"
				}
				entries[j].MessageGroupId = &groupId
			}
		}

		start := time.Now()
		output, err := client.PublishBatch(ctx, &sns.PublishBatchInput{
			TopicArn:                   &topicARN,
			PublishBatchRequestEntries: entries,
		})
		// Observer sees each underlying PublishBatch call; attribute to
		// the batch's first message.
		p.fireOnSend(u, batch[0], err, start)
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
