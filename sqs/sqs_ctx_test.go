package sqs

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"oss.nandlabs.io/golly/messaging"
)

// fakeSQSClient is a hand-rolled stub of the SQS client surface the
// provider relies on. Tests inject it via the package-level resolveClient
// var so we never hit AWS / LocalStack.
type fakeSQSClient struct {
	mu sync.Mutex

	sendCalls  []*awssqs.SendMessageInput
	batchCalls []*awssqs.SendMessageBatchInput
	recvCalls  []*awssqs.ReceiveMessageInput

	// sendFn overrides SendMessage behavior.
	sendFn func(ctx context.Context, in *awssqs.SendMessageInput) (*awssqs.SendMessageOutput, error)
	// recvFn overrides ReceiveMessage behavior; nil returns a canned msg.
	recvFn func(ctx context.Context, in *awssqs.ReceiveMessageInput) (*awssqs.ReceiveMessageOutput, error)
}

func (f *fakeSQSClient) SendMessage(ctx context.Context, in *awssqs.SendMessageInput, _ ...func(*awssqs.Options)) (*awssqs.SendMessageOutput, error) {
	f.mu.Lock()
	f.sendCalls = append(f.sendCalls, in)
	f.mu.Unlock()
	if f.sendFn != nil {
		return f.sendFn(ctx, in)
	}
	id := "msg-id"
	return &awssqs.SendMessageOutput{MessageId: &id}, nil
}

func (f *fakeSQSClient) SendMessageBatch(ctx context.Context, in *awssqs.SendMessageBatchInput, _ ...func(*awssqs.Options)) (*awssqs.SendMessageBatchOutput, error) {
	f.mu.Lock()
	f.batchCalls = append(f.batchCalls, in)
	f.mu.Unlock()
	successful := make([]types.SendMessageBatchResultEntry, len(in.Entries))
	for i, e := range in.Entries {
		id := *e.Id
		msgId := id
		successful[i] = types.SendMessageBatchResultEntry{Id: &id, MessageId: &msgId}
	}
	return &awssqs.SendMessageBatchOutput{Successful: successful}, nil
}

func (f *fakeSQSClient) ReceiveMessage(ctx context.Context, in *awssqs.ReceiveMessageInput, _ ...func(*awssqs.Options)) (*awssqs.ReceiveMessageOutput, error) {
	f.mu.Lock()
	f.recvCalls = append(f.recvCalls, in)
	f.mu.Unlock()
	if f.recvFn != nil {
		return f.recvFn(ctx, in)
	}
	body := "hello"
	rh := "receipt"
	return &awssqs.ReceiveMessageOutput{
		Messages: []types.Message{{Body: &body, ReceiptHandle: &rh}},
	}, nil
}

func (f *fakeSQSClient) DeleteMessage(ctx context.Context, in *awssqs.DeleteMessageInput, _ ...func(*awssqs.Options)) (*awssqs.DeleteMessageOutput, error) {
	return &awssqs.DeleteMessageOutput{}, nil
}

func (f *fakeSQSClient) ChangeMessageVisibility(ctx context.Context, in *awssqs.ChangeMessageVisibilityInput, _ ...func(*awssqs.Options)) (*awssqs.ChangeMessageVisibilityOutput, error) {
	return &awssqs.ChangeMessageVisibilityOutput{}, nil
}

func (f *fakeSQSClient) GetQueueUrl(ctx context.Context, in *awssqs.GetQueueUrlInput, _ ...func(*awssqs.Options)) (*awssqs.GetQueueUrlOutput, error) {
	q := "http://fake/" + *in.QueueName
	return &awssqs.GetQueueUrlOutput{QueueUrl: &q}, nil
}

// withFakeClient installs a fake sqsAPI + queue URL and restores the
// previous resolveClient on cleanup.
func withFakeClient(t *testing.T, client sqsAPI, queueURL string) {
	t.Helper()
	prev := resolveClient
	resolveClient = func(u *url.URL) (sqsAPI, string, error) {
		return client, queueURL, nil
	}
	t.Cleanup(func() { resolveClient = prev })
}

// recordingObserver captures OnSend / OnReceive events for assertions.
type recordingObserver struct {
	mu       sync.Mutex
	sends    []observedSend
	receives []observedReceive
}

type observedSend struct {
	u       *url.URL
	msg     messaging.Message
	err     error
	latency time.Duration
}

type observedReceive struct {
	u   *url.URL
	msg messaging.Message
	err error
}

func (r *recordingObserver) OnSend(u *url.URL, m messaging.Message, err error, latency time.Duration) {
	r.mu.Lock()
	r.sends = append(r.sends, observedSend{u, m, err, latency})
	r.mu.Unlock()
}

func (r *recordingObserver) OnReceive(u *url.URL, m messaging.Message, err error) {
	r.mu.Lock()
	r.receives = append(r.receives, observedReceive{u, m, err})
	r.mu.Unlock()
}

func (r *recordingObserver) OnAck(*url.URL, messaging.Message)         {}
func (r *recordingObserver) OnNack(*url.URL, messaging.Message, bool)  {}

func (r *recordingObserver) sendCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sends)
}

func (r *recordingObserver) receiveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.receives)
}

// newProviderMsg returns a *MessageSQS (which embeds *BaseMessage → Keyed)
// with the given body wired to the provider.
func newProviderMsg(t *testing.T, p *Provider, body string) messaging.Message {
	t.Helper()
	m, err := p.NewMessage(SQSScheme)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	if _, err := m.SetBodyStr(body); err != nil {
		t.Fatalf("SetBodyStr: %v", err)
	}
	return m
}

// ---- Interface satisfaction ------------------------------------------------

func TestProvider_SatisfiesV17MessagingInterfaces(t *testing.T) {
	p := &Provider{}
	if _, ok := interface{}(p).(messaging.ProducerCtx); !ok {
		t.Fatal("*Provider does not satisfy messaging.ProducerCtx")
	}
	if _, ok := interface{}(p).(messaging.ReceiverCtx); !ok {
		t.Fatal("*Provider does not satisfy messaging.ReceiverCtx")
	}
	if _, ok := interface{}(p).(messaging.ObservableProvider); !ok {
		t.Fatal("*Provider does not satisfy messaging.ObservableProvider")
	}
}

// ---- ProducerCtx: cancellation --------------------------------------------

func TestSendCtx_CancellationPropagates(t *testing.T) {
	p := &Provider{}

	// Fake SendMessage that respects ctx cancellation.
	fake := &fakeSQSClient{
		sendFn: func(ctx context.Context, _ *awssqs.SendMessageInput) (*awssqs.SendMessageOutput, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(5 * time.Second):
				t.Error("send did not observe cancellation")
				return nil, errors.New("timeout")
			}
		},
	}
	withFakeClient(t, fake, "http://fake/test-queue")

	u, _ := url.Parse("sqs://test-queue")
	msg := newProviderMsg(t, p, "payload")

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel almost immediately so SendCtx returns quickly.
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := p.SendCtx(ctx, u, msg)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected ctx-cancellation error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected error to wrap context.Canceled, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("SendCtx took too long to observe cancellation: %v", elapsed)
	}
}

// ---- Keyed → FIFO MessageGroupId ------------------------------------------

func TestSendCtx_KeyedMapsToMessageGroupId_OnFIFO(t *testing.T) {
	p := &Provider{}
	fake := &fakeSQSClient{}
	fifoURL := "http://fake/orders.fifo"
	withFakeClient(t, fake, fifoURL)

	u, _ := url.Parse("sqs://orders.fifo")
	msg := newProviderMsg(t, p, "keyed payload")
	// MessageSQS embeds *BaseMessage which implements Keyed.
	if k, ok := msg.(messaging.Keyed); ok {
		k.SetRoutingKey("tenant-42")
	} else {
		t.Fatal("provider message does not implement messaging.Keyed")
	}

	if err := p.SendCtx(context.Background(), u, msg); err != nil {
		t.Fatalf("SendCtx: %v", err)
	}

	if len(fake.sendCalls) != 1 {
		t.Fatalf("expected 1 SendMessage call, got %d", len(fake.sendCalls))
	}
	got := fake.sendCalls[0].MessageGroupId
	if got == nil {
		t.Fatal("expected MessageGroupId to be set on FIFO queue, got nil")
	}
	if *got != "tenant-42" {
		t.Fatalf("expected MessageGroupId=tenant-42, got %q", *got)
	}
}

func TestSendCtx_KeyedFIFO_EmptyKeyDefaultsToDefault(t *testing.T) {
	p := &Provider{}
	fake := &fakeSQSClient{}
	withFakeClient(t, fake, "http://fake/orders.fifo")

	u, _ := url.Parse("sqs://orders.fifo")
	msg := newProviderMsg(t, p, "unkeyed payload") // Keyed but empty routing key

	if err := p.SendCtx(context.Background(), u, msg); err != nil {
		t.Fatalf("SendCtx: %v", err)
	}
	if len(fake.sendCalls) != 1 {
		t.Fatalf("expected 1 SendMessage call, got %d", len(fake.sendCalls))
	}
	got := fake.sendCalls[0].MessageGroupId
	if got == nil || *got != defaultFIFOGroupId {
		var s string
		if got != nil {
			s = *got
		}
		t.Fatalf("expected MessageGroupId=%q, got %q", defaultFIFOGroupId, s)
	}
}

func TestSendCtx_KeyedIgnoredOnStandard(t *testing.T) {
	p := &Provider{}
	fake := &fakeSQSClient{}
	withFakeClient(t, fake, "http://fake/orders")

	u, _ := url.Parse("sqs://orders")
	msg := newProviderMsg(t, p, "keyed payload")
	if k, ok := msg.(messaging.Keyed); ok {
		k.SetRoutingKey("tenant-42")
	}

	if err := p.SendCtx(context.Background(), u, msg); err != nil {
		t.Fatalf("SendCtx: %v", err)
	}
	if len(fake.sendCalls) != 1 {
		t.Fatalf("expected 1 SendMessage call, got %d", len(fake.sendCalls))
	}
	if fake.sendCalls[0].MessageGroupId != nil {
		t.Fatalf("expected MessageGroupId=nil on standard queue, got %q", *fake.sendCalls[0].MessageGroupId)
	}
}

// ---- Observer hooks -------------------------------------------------------

func TestObserver_OnSend_FiresWithLatency(t *testing.T) {
	p := &Provider{}
	obs := &recordingObserver{}
	p.SetObserver(obs)

	// Give the fake a small delay so latency is non-zero.
	fake := &fakeSQSClient{
		sendFn: func(ctx context.Context, _ *awssqs.SendMessageInput) (*awssqs.SendMessageOutput, error) {
			time.Sleep(2 * time.Millisecond)
			id := "abc"
			return &awssqs.SendMessageOutput{MessageId: &id}, nil
		},
	}
	withFakeClient(t, fake, "http://fake/orders")

	u, _ := url.Parse("sqs://orders")
	msg := newProviderMsg(t, p, "hi")

	if err := p.SendCtx(context.Background(), u, msg); err != nil {
		t.Fatalf("SendCtx: %v", err)
	}
	if obs.sendCount() != 1 {
		t.Fatalf("expected 1 OnSend, got %d", obs.sendCount())
	}
	got := obs.sends[0]
	if got.u == nil || got.u.String() != u.String() {
		t.Fatalf("OnSend URL mismatch: got %v want %v", got.u, u)
	}
	if got.msg != msg {
		t.Fatalf("OnSend msg mismatch: got %v want %v", got.msg, msg)
	}
	if got.err != nil {
		t.Fatalf("OnSend err mismatch: got %v want nil", got.err)
	}
	if got.latency <= 0 {
		t.Fatalf("OnSend latency must be > 0, got %v", got.latency)
	}
}

func TestObserver_OnSend_FiresOnError(t *testing.T) {
	p := &Provider{}
	obs := &recordingObserver{}
	p.SetObserver(obs)

	boom := errors.New("boom")
	fake := &fakeSQSClient{
		sendFn: func(ctx context.Context, _ *awssqs.SendMessageInput) (*awssqs.SendMessageOutput, error) {
			return nil, boom
		},
	}
	withFakeClient(t, fake, "http://fake/orders")

	u, _ := url.Parse("sqs://orders")
	msg := newProviderMsg(t, p, "hi")

	err := p.SendCtx(context.Background(), u, msg)
	if err == nil {
		t.Fatal("expected send error, got nil")
	}
	if obs.sendCount() != 1 {
		t.Fatalf("expected 1 OnSend on error, got %d", obs.sendCount())
	}
	if !errors.Is(obs.sends[0].err, boom) {
		t.Fatalf("OnSend err should wrap %v, got %v", boom, obs.sends[0].err)
	}
}

func TestObserver_OnReceive_FiresPerMessage(t *testing.T) {
	p := &Provider{}
	obs := &recordingObserver{}
	p.SetObserver(obs)

	body1 := "one"
	body2 := "two"
	rh1 := "r1"
	rh2 := "r2"
	fake := &fakeSQSClient{
		recvFn: func(ctx context.Context, _ *awssqs.ReceiveMessageInput) (*awssqs.ReceiveMessageOutput, error) {
			return &awssqs.ReceiveMessageOutput{Messages: []types.Message{
				{Body: &body1, ReceiptHandle: &rh1},
				{Body: &body2, ReceiptHandle: &rh2},
			}}, nil
		},
	}
	withFakeClient(t, fake, "http://fake/orders")

	u, _ := url.Parse("sqs://orders")
	msgs, err := p.ReceiveBatchCtx(context.Background(), u)
	if err != nil {
		t.Fatalf("ReceiveBatchCtx: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if obs.receiveCount() != 2 {
		t.Fatalf("expected 2 OnReceive events, got %d", obs.receiveCount())
	}
	for i, r := range obs.receives {
		if r.err != nil {
			t.Fatalf("event %d unexpected err: %v", i, r.err)
		}
		if r.msg == nil {
			t.Fatalf("event %d msg is nil", i)
		}
		if r.u == nil || r.u.String() != u.String() {
			t.Fatalf("event %d url mismatch: %v", i, r.u)
		}
	}
}

func TestObserver_Nil_IsSafe(t *testing.T) {
	p := &Provider{} // observer never set
	fake := &fakeSQSClient{}
	withFakeClient(t, fake, "http://fake/orders")

	u, _ := url.Parse("sqs://orders")
	msg := newProviderMsg(t, p, "hi")

	// Round-trip a Send and a Receive; hook sites must not panic when the
	// observer is nil.
	if err := p.SendCtx(context.Background(), u, msg); err != nil {
		t.Fatalf("SendCtx: %v", err)
	}
	if _, err := p.ReceiveCtx(context.Background(), u); err != nil {
		t.Fatalf("ReceiveCtx: %v", err)
	}

	// Explicitly clear observer with nil, then send again.
	p.SetObserver(nil)
	if err := p.SendCtx(context.Background(), u, msg); err != nil {
		t.Fatalf("SendCtx after nil observer: %v", err)
	}
}

func TestObserver_SetObserver_ReplaceAndClear(t *testing.T) {
	p := &Provider{}
	fake := &fakeSQSClient{}
	withFakeClient(t, fake, "http://fake/orders")

	u, _ := url.Parse("sqs://orders")
	msg := newProviderMsg(t, p, "hi")

	first := &recordingObserver{}
	second := &recordingObserver{}
	p.SetObserver(first)
	if err := p.SendCtx(context.Background(), u, msg); err != nil {
		t.Fatal(err)
	}
	p.SetObserver(second)
	if err := p.SendCtx(context.Background(), u, msg); err != nil {
		t.Fatal(err)
	}
	p.SetObserver(nil)
	if err := p.SendCtx(context.Background(), u, msg); err != nil {
		t.Fatal(err)
	}

	if first.sendCount() != 1 {
		t.Fatalf("first observer: want 1 send, got %d", first.sendCount())
	}
	if second.sendCount() != 1 {
		t.Fatalf("second observer: want 1 send, got %d", second.sendCount())
	}
}

// ---- Non-ctx delegation still fires observer -----------------------------

func TestSend_DelegatesToSendCtx(t *testing.T) {
	p := &Provider{}
	obs := &recordingObserver{}
	p.SetObserver(obs)
	fake := &fakeSQSClient{}
	withFakeClient(t, fake, "http://fake/orders")

	u, _ := url.Parse("sqs://orders")
	msg := newProviderMsg(t, p, "hi")

	if err := p.Send(u, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(fake.sendCalls) != 1 {
		t.Fatalf("expected 1 send call, got %d", len(fake.sendCalls))
	}
	if obs.sendCount() != 1 {
		t.Fatalf("expected 1 OnSend from delegated Send, got %d", obs.sendCount())
	}
}

// ---- SendBatchCtx: keyed FIFO ---------------------------------------------

func TestSendBatchCtx_KeyedFIFO_PerMessageGroupId(t *testing.T) {
	p := &Provider{}
	fake := &fakeSQSClient{}
	withFakeClient(t, fake, "http://fake/orders.fifo")

	u, _ := url.Parse("sqs://orders.fifo")
	m1 := newProviderMsg(t, p, "m1")
	m1.(messaging.Keyed).SetRoutingKey("A")
	m2 := newProviderMsg(t, p, "m2")
	m2.(messaging.Keyed).SetRoutingKey("B")

	if err := p.SendBatchCtx(context.Background(), u, []messaging.Message{m1, m2}); err != nil {
		t.Fatalf("SendBatchCtx: %v", err)
	}
	if len(fake.batchCalls) != 1 {
		t.Fatalf("expected 1 batch call, got %d", len(fake.batchCalls))
	}
	entries := fake.batchCalls[0].Entries
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].MessageGroupId == nil || *entries[0].MessageGroupId != "A" {
		t.Fatalf("entry[0] group: want A, got %v", entries[0].MessageGroupId)
	}
	if entries[1].MessageGroupId == nil || *entries[1].MessageGroupId != "B" {
		t.Fatalf("entry[1] group: want B, got %v", entries[1].MessageGroupId)
	}
}

// ---- ReceiveBatchCtx cancellation ----------------------------------------

func TestReceiveCtx_CancellationPropagates(t *testing.T) {
	p := &Provider{}
	var blocked atomic.Bool
	fake := &fakeSQSClient{
		recvFn: func(ctx context.Context, _ *awssqs.ReceiveMessageInput) (*awssqs.ReceiveMessageOutput, error) {
			blocked.Store(true)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(5 * time.Second):
				return nil, errors.New("timeout")
			}
		},
	}
	withFakeClient(t, fake, "http://fake/orders")

	u, _ := url.Parse("sqs://orders")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for !blocked.Load() {
			time.Sleep(1 * time.Millisecond)
		}
		cancel()
	}()

	_, err := p.ReceiveCtx(ctx, u)
	if err == nil {
		t.Fatal("expected error on cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
