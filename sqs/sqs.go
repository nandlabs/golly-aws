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

	// defaultFIFOGroupId is the fallback MessageGroupId when a Keyed
	// message on a FIFO queue exposes an empty routing key.
	defaultFIFOGroupId = "default"
)

var sqsSchemes = []string{SQSScheme}

// sqsAPI is the subset of the AWS SQS client surface the provider relies on.
// It exists so tests can inject a fake without spinning up LocalStack — the
// concrete *sqs.Client already satisfies this interface.
type sqsAPI interface {
	SendMessage(ctx context.Context, params *sqs.SendMessageInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
	SendMessageBatch(ctx context.Context, params *sqs.SendMessageBatchInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageBatchOutput, error)
	ReceiveMessage(ctx context.Context, params *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, params *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
	ChangeMessageVisibility(ctx context.Context, params *sqs.ChangeMessageVisibilityInput, optFns ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error)
	GetQueueUrl(ctx context.Context, params *sqs.GetQueueUrlInput, optFns ...func(*sqs.Options)) (*sqs.GetQueueUrlOutput, error)
	// GetQueueAttributes lets the broker-options plumbing verify a
	// queue's RedrivePolicy against DeadLetter / MaxDeliveryAttempts
	// without going through queueAttributesGetter separately.
	GetQueueAttributes(ctx context.Context, params *sqs.GetQueueAttributesInput, optFns ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error)
}

// resolveClient returns an SQS API client and the resolved queue URL for u.
// It is a package-level var so tests can inject a fake client + queue URL
// without touching the AWS SDK. Production callers get the LocalStack /
// real-AWS wiring via getSQSClient + resolveQueueURL.
var resolveClient = func(u *url.URL) (sqsAPI, string, error) {
	client, err := getSQSClient(u)
	if err != nil {
		return nil, "", err
	}
	queueURL, err := resolveQueueURL(client, u)
	if err != nil {
		return nil, "", err
	}
	return client, queueURL, nil
}

// sqsListenerEntry tracks a single registered listener so it can be
// individually cancelled by name via RemoveNamedListener, or as part of
// a per-host group via RemoveListeners.
type sqsListenerEntry struct {
	name   string // empty for unnamed listeners
	cancel context.CancelFunc
}

// Provider implements the messaging.Provider interface for AWS SQS.
// It also implements messaging.ListenerRemover so callers can detach
// previously-registered listeners by URL or by named group without
// closing the entire provider.
type Provider struct {
	closed    atomic.Bool
	mu        sync.Mutex
	listeners map[string][]sqsListenerEntry // host → registered listeners

	obsMu    sync.RWMutex
	observer messaging.Observer
}

// Compile-time interface assertions.
var (
	_ messaging.Producer           = (*Provider)(nil)
	_ messaging.ProducerCtx        = (*Provider)(nil)
	_ messaging.Receiver           = (*Provider)(nil)
	_ messaging.ReceiverCtx        = (*Provider)(nil)
	_ messaging.ObservableProvider = (*Provider)(nil)
)

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

// SetObserver installs (or clears, with nil) the metrics / tracing observer.
// It is safe to call at any point in the provider's lifecycle; each hook
// site snapshots the current observer under a read lock.
func (p *Provider) SetObserver(obs messaging.Observer) {
	p.obsMu.Lock()
	p.observer = obs
	p.obsMu.Unlock()
}

// currentObserver returns the currently installed observer (may be nil).
func (p *Provider) currentObserver() messaging.Observer {
	p.obsMu.RLock()
	o := p.observer
	p.obsMu.RUnlock()
	return o
}

func (p *Provider) fireOnSend(u *url.URL, msg messaging.Message, err error, latency time.Duration) {
	if o := p.currentObserver(); o != nil {
		o.OnSend(u, msg, err, latency)
	}
}

func (p *Provider) fireOnReceive(u *url.URL, msg messaging.Message, err error) {
	if o := p.currentObserver(); o != nil {
		o.OnReceive(u, msg, err)
	}
}

// resolveGroupId returns the MessageGroupId to attach to a Send on the given
// queue. Rules:
//   - Explicit OptMessageGroupId option → always wins (preserves the prior
//     opt-driven contract for callers who set it deliberately).
//   - Standard queue + no explicit option → nil, routing keys ignored
//     silently (SQS Standard has no message-group concept).
//   - FIFO queue + Keyed message → routing key becomes the group id; empty
//     routing keys collapse to "default".
//   - FIFO queue + unkeyed message + no option → nil (SQS will reject
//     server-side; caller must set the option or use a Keyed message).
func resolveGroupId(msg messaging.Message, queueURL string, explicit *string) *string {
	if explicit != nil && *explicit != "" {
		return explicit
	}
	if !isFIFOQueue(queueURL) {
		return nil
	}
	if _, ok := msg.(messaging.Keyed); ok {
		key := messaging.RoutingKeyOf(msg)
		if key == "" {
			key = defaultFIFOGroupId
		}
		return &key
	}
	return nil
}

// Send sends a single message to the SQS queue.
// URL format: sqs://queue-name
func (p *Provider) Send(u *url.URL, msg messaging.Message, options ...messaging.Option) error {
	return p.SendCtx(context.Background(), u, msg, options...)
}

// SendCtx is the context-aware variant of Send. It passes ctx through to
// the underlying AWS SDK v2 call so cancellation and deadlines propagate.
func (p *Provider) SendCtx(ctx context.Context, u *url.URL, msg messaging.Message, options ...messaging.Option) error {
	client, queueURL, err := resolveClient(u)
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

	// Validate broker-targeted options (golly v1.6.0) before touching AWS.
	// On the send path we only need parse-time validation — DLQ / redrive
	// checks are receive-side and run in AddListener / ReceiveBatch.
	if _, err := parseBrokerOptions(optResolver, isFIFOQueue(u.Host)); err != nil {
		return err
	}

	var explicitGroupId *string
	if v, ok := optResolver.Get(OptMessageGroupId); ok {
		groupId := v.(string)
		explicitGroupId = &groupId
	}
	input.MessageGroupId = resolveGroupId(msg, queueURL, explicitGroupId)
	if v, ok := optResolver.Get(OptMessageDeduplicationId); ok {
		dedupId := v.(string)
		input.MessageDeduplicationId = &dedupId
	}
	if v, ok := optResolver.Get(OptDelaySeconds); ok {
		input.DelaySeconds = int32(v.(int))
	}

	start := time.Now()
	_, sendErr := client.SendMessage(ctx, input)
	latency := time.Since(start)
	if sendErr != nil {
		sendErr = fmt.Errorf("sqs: send failed: %w", sendErr)
	}
	p.fireOnSend(u, msg, sendErr, latency)
	if sendErr != nil {
		return sendErr
	}

	logger.InfoF("SQS message sent to %s", queueURL)
	return nil
}

// SendBatch sends a batch of messages to the SQS queue.
// SQS supports up to 10 messages per batch. If more than 10 messages are provided,
// they will be sent in multiple batches.
func (p *Provider) SendBatch(u *url.URL, msgs []messaging.Message, options ...messaging.Option) error {
	return p.SendBatchCtx(context.Background(), u, msgs, options...)
}

// SendBatchCtx is the context-aware variant of SendBatch. ctx is propagated
// into every SendMessageBatch call; observer hooks fire per message.
func (p *Provider) SendBatchCtx(ctx context.Context, u *url.URL, msgs []messaging.Message, options ...messaging.Option) error {
	if len(msgs) == 0 {
		return nil
	}

	client, queueURL, err := resolveClient(u)
	if err != nil {
		return err
	}

	optResolver := messaging.NewOptionsResolver(options...)
	var explicitGroupId *string
	if v, ok := optResolver.Get(OptMessageGroupId); ok {
		groupId := v.(string)
		explicitGroupId = &groupId
	}
	var explicitDedupId *string
	if v, ok := optResolver.Get(OptMessageDeduplicationId); ok {
		dedupId := v.(string)
		explicitDedupId = &dedupId
	}
	var explicitDelay *int32
	if v, ok := optResolver.Get(OptDelaySeconds); ok {
		d := int32(v.(int))
		explicitDelay = &d
	}

	// Validate broker-targeted options (golly v1.6.0) before batching.
	if _, err := parseBrokerOptions(optResolver, isFIFOQueue(u.Host)); err != nil {
		return err
	}

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
			entries[j].MessageGroupId = resolveGroupId(msg, queueURL, explicitGroupId)
			if explicitDedupId != nil {
				entries[j].MessageDeduplicationId = explicitDedupId
			}
			if explicitDelay != nil {
				entries[j].DelaySeconds = *explicitDelay
			}
		}

		start := time.Now()
		output, sendErr := client.SendMessageBatch(ctx, &sqs.SendMessageBatchInput{
			QueueUrl: &queueURL,
			Entries:  entries,
		})
		latency := time.Since(start)

		if sendErr != nil {
			sendErr = fmt.Errorf("sqs: batch send failed: %w", sendErr)
			// Fire per-message failure so callers can attribute errors.
			for _, m := range batch {
				p.fireOnSend(u, m, sendErr, latency)
			}
			return sendErr
		}

		var batchErr error
		if len(output.Failed) > 0 {
			batchErr = fmt.Errorf("sqs: %d messages failed in batch send: %s",
				len(output.Failed), *output.Failed[0].Message)
		}
		for _, m := range batch {
			p.fireOnSend(u, m, batchErr, latency)
		}
		if batchErr != nil {
			return batchErr
		}

		logger.InfoF("SQS batch sent %d messages to %s", len(batch), queueURL)
	}

	return nil
}

// Receive receives a single message from the SQS queue.
// Supports options: WaitTimeSeconds, VisibilityTimeout.
func (p *Provider) Receive(u *url.URL, options ...messaging.Option) (messaging.Message, error) {
	return p.ReceiveCtx(context.Background(), u, options...)
}

// ReceiveCtx is the context-aware variant of Receive. ctx propagates into
// the long-poll ReceiveMessage call so cancellation aborts the wait.
func (p *Provider) ReceiveCtx(ctx context.Context, u *url.URL, options ...messaging.Option) (messaging.Message, error) {
	client, queueURL, err := resolveClient(u)
	if err != nil {
		return nil, err
	}

	input := &sqs.ReceiveMessageInput{
		QueueUrl:              &queueURL,
		MaxNumberOfMessages:   1,
		MessageAttributeNames: []string{"All"},
	}

	optResolver := messaging.NewOptionsResolver(options...)

	// Broker-targeted options (golly v1.6.0). AckTimeout populates
	// VisibilityTimeout as a base; the SQS-specific OptVisibilityTimeout
	// takes precedence below when both are set.
	bo, err := parseBrokerOptions(optResolver, isFIFOQueue(u.Host))
	if err != nil {
		return nil, err
	}
	if bo.hasVisibilityTimeout {
		input.VisibilityTimeout = bo.visibilityTimeout
	}
	if err := bo.validateRedrivePolicy(context.Background(), client, queueURL); err != nil {
		return nil, err
	}

	if v, ok := optResolver.Get(OptWaitTimeSeconds); ok {
		input.WaitTimeSeconds = int32(v.(int))
	} else {
		input.WaitTimeSeconds = 5 // default long poll
	}
	if v, ok := optResolver.Get(OptVisibilityTimeout); ok {
		input.VisibilityTimeout = int32(v.(int))
	}

	output, err := client.ReceiveMessage(ctx, input)
	if err != nil {
		wrapped := fmt.Errorf("sqs: receive failed: %w", err)
		p.fireOnReceive(u, nil, wrapped)
		return nil, wrapped
	}

	if len(output.Messages) == 0 {
		notFound := fmt.Errorf("sqs: no messages available")
		return nil, notFound
	}

	msg := p.toMessage(output.Messages[0], queueURL)
	p.fireOnReceive(u, msg, nil)
	return msg, nil
}

// ReceiveBatch receives a batch of messages from the SQS queue.
// Supports options: BatchSize (default 10), WaitTimeSeconds, VisibilityTimeout.
func (p *Provider) ReceiveBatch(u *url.URL, options ...messaging.Option) ([]messaging.Message, error) {
	return p.ReceiveBatchCtx(context.Background(), u, options...)
}

// ReceiveBatchCtx is the context-aware variant of ReceiveBatch. ctx
// propagates into the underlying ReceiveMessage call.
func (p *Provider) ReceiveBatchCtx(ctx context.Context, u *url.URL, options ...messaging.Option) ([]messaging.Message, error) {
	client, queueURL, err := resolveClient(u)
	if err != nil {
		return nil, err
	}

	optResolver := messaging.NewOptionsResolver(options...)

	// Broker-targeted options (golly v1.6.0).
	bo, err := parseBrokerOptions(optResolver, isFIFOQueue(u.Host))
	if err != nil {
		return nil, err
	}
	if err := bo.validateRedrivePolicy(context.Background(), client, queueURL); err != nil {
		return nil, err
	}

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

	if bo.hasVisibilityTimeout {
		input.VisibilityTimeout = bo.visibilityTimeout
	}
	if v, ok := optResolver.Get(OptWaitTimeSeconds); ok {
		input.WaitTimeSeconds = int32(v.(int))
	} else {
		input.WaitTimeSeconds = 5
	}
	if v, ok := optResolver.Get(OptVisibilityTimeout); ok {
		input.VisibilityTimeout = int32(v.(int))
	}

	output, err := client.ReceiveMessage(ctx, input)
	if err != nil {
		wrapped := fmt.Errorf("sqs: receive batch failed: %w", err)
		p.fireOnReceive(u, nil, wrapped)
		return nil, wrapped
	}

	if len(output.Messages) == 0 {
		return nil, fmt.Errorf("sqs: no messages available")
	}

	msgs := make([]messaging.Message, len(output.Messages))
	for i, sqsMsg := range output.Messages {
		msgs[i] = p.toMessage(sqsMsg, queueURL)
		p.fireOnReceive(u, msgs[i], nil)
	}

	return msgs, nil
}

// AddListener registers a listener that continuously polls the SQS queue for messages.
// The listener runs in a goroutine and can be stopped by calling Close on the provider.
// Supports options: WaitTimeSeconds, VisibilityTimeout, Timeout (total listener duration in seconds).
func (p *Provider) AddListener(u *url.URL, listener func(msg messaging.Message), options ...messaging.Option) error {
	return p.AddListenerCtx(context.Background(), u, listener, options...)
}

// AddListenerCtx is the context-aware variant of AddListener. The listener
// goroutine derives its polling context from ctx, so cancelling ctx tears
// the listener down (in addition to Provider.Close).
func (p *Provider) AddListenerCtx(ctx context.Context, u *url.URL, listener func(msg messaging.Message), options ...messaging.Option) error {
	client, queueURL, err := resolveClient(u)
	if err != nil {
		return err
	}

	optResolver := messaging.NewOptionsResolver(options...)

	// Broker-targeted options (golly v1.6.0). Parse + validate before we
	// spawn the listener goroutine — an invalid combination should surface
	// synchronously to the caller, not asynchronously via a log line.
	bo, err := parseBrokerOptions(optResolver, isFIFOQueue(u.Host))
	if err != nil {
		return err
	}
	if err := bo.validateRedrivePolicy(context.Background(), client, queueURL); err != nil {
		return err
	}

	waitTime := int32(5)
	if v, ok := optResolver.Get(OptWaitTimeSeconds); ok {
		waitTime = int32(v.(int))
	}

	var visibilityTimeout int32
	if bo.hasVisibilityTimeout {
		visibilityTimeout = bo.visibilityTimeout
	}
	if v, ok := optResolver.Get(OptVisibilityTimeout); ok {
		visibilityTimeout = int32(v.(int))
	}

	pollCtx, cancel := context.WithCancel(ctx)
	if v, ok := optResolver.Get(OptTimeout); ok {
		timeout := time.Duration(v.(int)) * time.Second
		pollCtx, cancel = context.WithTimeout(ctx, timeout)
	}

	// Recognise the same "NamedListener" option that golly's LocalProvider
	// uses so RemoveNamedListener works consistently across providers.
	var listenerName string
	if v, ok := messaging.ResolveOptValue[string]("NamedListener", optResolver); ok {
		listenerName = v
	}

	p.mu.Lock()
	if p.listeners == nil {
		p.listeners = make(map[string][]sqsListenerEntry)
	}
	p.listeners[u.Host] = append(p.listeners[u.Host], sqsListenerEntry{name: listenerName, cancel: cancel})
	p.mu.Unlock()

	go func() {
		defer cancel()
		logger.InfoF("SQS listener started for %s", queueURL)
		for {
			if p.closed.Load() {
				return
			}

			select {
			case <-pollCtx.Done():
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

			output, err := client.ReceiveMessage(pollCtx, input)
			if err != nil {
				if pollCtx.Err() != nil {
					return // context cancelled
				}
				p.fireOnReceive(u, nil, err)
				logger.ErrorF("SQS listener receive error: %v", err)
				time.Sleep(time.Second) // backoff on error
				continue
			}

			for _, sqsMsg := range output.Messages {
				msg := p.toMessage(sqsMsg, queueURL)
				p.fireOnReceive(u, msg, nil)
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
	for _, entries := range p.listeners {
		for _, e := range entries {
			e.cancel()
		}
	}
	p.listeners = nil
	return nil
}

// RemoveListeners cancels every listener registered for the URL. Other
// URLs are untouched. Idempotent — returns nil if no listeners are
// registered for the URL. Implements messaging.ListenerRemover.
func (p *Provider) RemoveListeners(u *url.URL) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	entries, ok := p.listeners[u.Host]
	if !ok {
		return nil
	}
	for _, e := range entries {
		e.cancel()
	}
	delete(p.listeners, u.Host)
	return nil
}

// RemoveNamedListener cancels listeners registered under the given name
// for the URL. Other listeners on the same URL (including unnamed)
// continue to receive. Idempotent.
// Implements messaging.ListenerRemover.
func (p *Provider) RemoveNamedListener(u *url.URL, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	entries, ok := p.listeners[u.Host]
	if !ok {
		return nil
	}
	kept := entries[:0]
	for _, e := range entries {
		if e.name == name {
			e.cancel()
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) == 0 {
		delete(p.listeners, u.Host)
	} else {
		p.listeners[u.Host] = kept
	}
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
