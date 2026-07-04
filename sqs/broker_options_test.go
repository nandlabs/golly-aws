package sqs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqsTypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"oss.nandlabs.io/golly/messaging"
)

// fakeQueueAttributesGetter is a stand-in for *sqs.Client used by the
// RedrivePolicy validation tests. It matches the queueAttributesGetter
// interface defined in broker_options.go.
type fakeQueueAttributesGetter struct {
	attrs map[string]string
	err   error
	calls int
}

func (f *fakeQueueAttributesGetter) GetQueueAttributes(_ context.Context, _ *sqs.GetQueueAttributesInput, _ ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &sqs.GetQueueAttributesOutput{Attributes: f.attrs}, nil
}

func TestSQS_AckTimeout_MapsToVisibilityTimeout(t *testing.T) {
	opts := messaging.NewOptionsBuilder().AddAckTimeout(45 * time.Second).Build()
	resolver := messaging.NewOptionsResolver(opts...)

	bo, err := parseBrokerOptions(resolver, false)
	if err != nil {
		t.Fatalf("parseBrokerOptions failed: %v", err)
	}
	if !bo.hasVisibilityTimeout {
		t.Fatal("expected hasVisibilityTimeout=true")
	}
	if bo.visibilityTimeout != 45 {
		t.Fatalf("expected visibilityTimeout=45, got %d", bo.visibilityTimeout)
	}

	// Zero/negative durations from the builder are dropped and never
	// reach the parser — but we also want a defensive parser-side check.
	badResolver := messaging.NewOptionsResolver(messaging.Option{Key: messaging.AckTimeoutOpt, Value: -1 * time.Second})
	if _, err := parseBrokerOptions(badResolver, false); err == nil {
		t.Fatal("expected error for negative AckTimeout")
	}

	// >12h should exceed SQS visibility timeout limit.
	overResolver := messaging.NewOptionsResolver(messaging.Option{Key: messaging.AckTimeoutOpt, Value: 13 * time.Hour})
	if _, err := parseBrokerOptions(overResolver, false); err == nil {
		t.Fatal("expected error for AckTimeout > 12h")
	}
}

func TestSQS_MaxDeliveryAttempts_ValidatesQueueRedrivePolicy(t *testing.T) {
	ctx := context.Background()
	resolver := messaging.NewOptionsResolver(
		messaging.NewOptionsBuilder().AddMaxDeliveryAttempts(5).Build()...,
	)
	bo, err := parseBrokerOptions(resolver, false)
	if err != nil {
		t.Fatalf("parseBrokerOptions failed: %v", err)
	}

	// Queue with a matching policy — accept.
	okClient := &fakeQueueAttributesGetter{attrs: map[string]string{
		string(sqsTypes.QueueAttributeNameRedrivePolicy): `{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:dlq","maxReceiveCount":5}`,
	}}
	if err := bo.validateRedrivePolicy(ctx, okClient, "http://localhost/q"); err != nil {
		t.Fatalf("expected policy to satisfy maxAttempts=5, got: %v", err)
	}
	if okClient.calls != 1 {
		t.Fatalf("expected 1 GetQueueAttributes call, got %d", okClient.calls)
	}

	// Queue with a lower maxReceiveCount — reject.
	lowClient := &fakeQueueAttributesGetter{attrs: map[string]string{
		string(sqsTypes.QueueAttributeNameRedrivePolicy): `{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:dlq","maxReceiveCount":2}`,
	}}
	err = bo.validateRedrivePolicy(ctx, lowClient, "http://localhost/q")
	if err == nil {
		t.Fatal("expected error when RedrivePolicy.maxReceiveCount < requested MaxDeliveryAttempts")
	}
	if !strings.Contains(err.Error(), "maxReceiveCount=2") || !strings.Contains(err.Error(), "MaxDeliveryAttempts=5") {
		t.Fatalf("expected error to mention both values, got: %v", err)
	}

	// Queue with no RedrivePolicy attribute — reject with creation hint.
	noPolicyClient := &fakeQueueAttributesGetter{attrs: map[string]string{}}
	err = bo.validateRedrivePolicy(ctx, noPolicyClient, "http://localhost/q")
	if err == nil {
		t.Fatal("expected error when queue has no RedrivePolicy")
	}
	if !strings.Contains(err.Error(), "queue creation") && !strings.Contains(err.Error(), "auto-create") {
		t.Fatalf("expected error to point at queue creation, got: %v", err)
	}

	// SDK-level error propagates as a wrapped error.
	brokenClient := &fakeQueueAttributesGetter{err: errors.New("boom")}
	err = bo.validateRedrivePolicy(ctx, brokenClient, "http://localhost/q")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected wrapped SDK error, got: %v", err)
	}

	// DeadLetter option is checked against RedrivePolicy.deadLetterTargetArn:
	// bare-name match against the ARN tail is accepted.
	dlResolver := messaging.NewOptionsResolver(
		messaging.NewOptionsBuilder().AddDeadLetter("dlq").Build()...,
	)
	dlBO, err := parseBrokerOptions(dlResolver, false)
	if err != nil {
		t.Fatalf("parseBrokerOptions for DLQ opts failed: %v", err)
	}
	dlOK := &fakeQueueAttributesGetter{attrs: map[string]string{
		string(sqsTypes.QueueAttributeNameRedrivePolicy): `{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:dlq","maxReceiveCount":3}`,
	}}
	if err := dlBO.validateRedrivePolicy(ctx, dlOK, "http://localhost/q"); err != nil {
		t.Fatalf("expected bare DLQ name to match ARN tail, got: %v", err)
	}
	dlMismatch := &fakeQueueAttributesGetter{attrs: map[string]string{
		string(sqsTypes.QueueAttributeNameRedrivePolicy): `{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:other","maxReceiveCount":3}`,
	}}
	if err := dlBO.validateRedrivePolicy(ctx, dlMismatch, "http://localhost/q"); err == nil {
		t.Fatal("expected DLQ mismatch error")
	}

	// Fast-path: no options → no SDK call.
	emptyBO, _ := parseBrokerOptions(messaging.NewOptionsResolver(), false)
	silent := &fakeQueueAttributesGetter{}
	if err := emptyBO.validateRedrivePolicy(ctx, silent, "http://localhost/q"); err != nil {
		t.Fatalf("expected no-op with no options, got: %v", err)
	}
	if silent.calls != 0 {
		t.Fatalf("expected 0 SDK calls when no options set, got %d", silent.calls)
	}
}

func TestSQS_ConsumerMode_RejectsPush(t *testing.T) {
	resolver := messaging.NewOptionsResolver(
		messaging.NewOptionsBuilder().AddConsumerMode(messaging.ConsumerPush).Build()...,
	)
	_, err := parseBrokerOptions(resolver, false)
	if err == nil {
		t.Fatal("expected error for ConsumerMode=ConsumerPush")
	}
	if !strings.Contains(err.Error(), "ConsumerPush") && !strings.Contains(err.Error(), "push") {
		t.Fatalf("expected error to mention push mode, got: %v", err)
	}
	if !strings.Contains(err.Error(), "pull") && !strings.Contains(err.Error(), "long-poll") {
		t.Fatalf("expected error to explain SQS is pull/long-poll, got: %v", err)
	}

	// ConsumerPull is accepted.
	pullResolver := messaging.NewOptionsResolver(
		messaging.NewOptionsBuilder().AddConsumerMode(messaging.ConsumerPull).Build()...,
	)
	if _, err := parseBrokerOptions(pullResolver, false); err != nil {
		t.Fatalf("expected ConsumerPull to be accepted, got: %v", err)
	}
}

func TestSQS_DeliveryGuarantee_RejectsAtMostOnce(t *testing.T) {
	// AtMostOnce: rejected on both Standard and FIFO — SQS is at-least-once.
	resolver := messaging.NewOptionsResolver(
		messaging.NewOptionsBuilder().AddDeliveryGuarantee(messaging.AtMostOnce).Build()...,
	)
	if _, err := parseBrokerOptions(resolver, false); err == nil {
		t.Fatal("expected error for DeliveryGuarantee=AtMostOnce on Standard queue")
	}
	if _, err := parseBrokerOptions(resolver, true); err == nil {
		t.Fatal("expected error for DeliveryGuarantee=AtMostOnce on FIFO queue too")
	}

	// AtLeastOnce is accepted.
	atLeastResolver := messaging.NewOptionsResolver(
		messaging.NewOptionsBuilder().AddDeliveryGuarantee(messaging.AtLeastOnce).Build()...,
	)
	if _, err := parseBrokerOptions(atLeastResolver, false); err != nil {
		t.Fatalf("expected AtLeastOnce to be accepted on Standard, got: %v", err)
	}

	// ExactlyOnce: rejected on Standard, accepted on FIFO.
	exactlyResolver := messaging.NewOptionsResolver(
		messaging.NewOptionsBuilder().AddDeliveryGuarantee(messaging.ExactlyOnce).Build()...,
	)
	if _, err := parseBrokerOptions(exactlyResolver, false); err == nil {
		t.Fatal("expected ExactlyOnce rejected on Standard SQS")
	}
	if _, err := parseBrokerOptions(exactlyResolver, true); err != nil {
		t.Fatalf("expected ExactlyOnce accepted on FIFO SQS, got: %v", err)
	}
}

func TestSQS_IsFIFOQueue(t *testing.T) {
	if !isFIFOQueue("orders.fifo") {
		t.Fatal("expected orders.fifo to be recognised as FIFO")
	}
	if isFIFOQueue("orders") {
		t.Fatal("expected bare orders to be standard")
	}
	if !isFIFOQueue("https://sqs.us-east-1.amazonaws.com/000000000000/orders.fifo") {
		t.Fatal("expected full FIFO URL to be recognised")
	}
}

func TestSQS_DLQIdentifierMatches(t *testing.T) {
	if !dlqIdentifierMatches("arn:aws:sqs:us-east-1:000000000000:dlq", "arn:aws:sqs:us-east-1:000000000000:dlq") {
		t.Fatal("expected exact ARN match")
	}
	if !dlqIdentifierMatches("arn:aws:sqs:us-east-1:000000000000:dlq", "dlq") {
		t.Fatal("expected bare-name to match ARN tail")
	}
	if dlqIdentifierMatches("arn:aws:sqs:us-east-1:000000000000:other", "dlq") {
		t.Fatal("expected mismatch between different names")
	}
}
