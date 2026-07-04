package sns

import (
	"strings"
	"testing"
	"time"

	"oss.nandlabs.io/golly/messaging"
)

func TestSNS_DeliveryGuarantee_RejectsExactlyOnce(t *testing.T) {
	resolver := messaging.NewOptionsResolver(
		messaging.NewOptionsBuilder().AddDeliveryGuarantee(messaging.ExactlyOnce).Build()...,
	)
	err := parseBrokerOptions(resolver)
	if err == nil {
		t.Fatal("expected error for DeliveryGuarantee=ExactlyOnce on SNS")
	}
	if !strings.Contains(err.Error(), "exactly-once") && !strings.Contains(err.Error(), "ExactlyOnce") {
		t.Fatalf("expected error to mention exactly-once, got: %v", err)
	}

	// AtLeastOnce is accepted (SNS default).
	atLeast := messaging.NewOptionsResolver(
		messaging.NewOptionsBuilder().AddDeliveryGuarantee(messaging.AtLeastOnce).Build()...,
	)
	if err := parseBrokerOptions(atLeast); err != nil {
		t.Fatalf("expected AtLeastOnce accepted, got: %v", err)
	}

	// AtMostOnce is downgraded to a warning — no error.
	atMost := messaging.NewOptionsResolver(
		messaging.NewOptionsBuilder().AddDeliveryGuarantee(messaging.AtMostOnce).Build()...,
	)
	if err := parseBrokerOptions(atMost); err != nil {
		t.Fatalf("expected AtMostOnce accepted (with warning), got: %v", err)
	}
}

func TestSNS_DeadLetterOpt_NotSupportedByPublisher(t *testing.T) {
	resolver := messaging.NewOptionsResolver(
		messaging.NewOptionsBuilder().AddDeadLetter("arn:aws:sqs:us-east-1:0:my-dlq").Build()...,
	)
	err := parseBrokerOptions(resolver)
	if err == nil {
		t.Fatal("expected DeadLetter to be rejected on SNS publisher")
	}
	if !strings.Contains(err.Error(), "subscription") && !strings.Contains(err.Error(), "RedrivePolicy") {
		t.Fatalf("expected error to point at subscription-level RedrivePolicy, got: %v", err)
	}
}

func TestSNS_OtherBrokerOptionsRejected(t *testing.T) {
	cases := []struct {
		name    string
		options []messaging.Option
	}{
		{
			name:    "MaxDeliveryAttempts",
			options: messaging.NewOptionsBuilder().AddMaxDeliveryAttempts(3).Build(),
		},
		{
			name:    "ConsumerMode",
			options: messaging.NewOptionsBuilder().AddConsumerMode(messaging.ConsumerPush).Build(),
		},
		{
			name:    "AckTimeout",
			options: messaging.NewOptionsBuilder().AddAckTimeout(30 * time.Second).Build(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := messaging.NewOptionsResolver(tc.options...)
			err := parseBrokerOptions(resolver)
			if err == nil {
				t.Fatalf("expected %s to be rejected on SNS publisher", tc.name)
			}
		})
	}
}

func TestSNS_NoBrokerOptions_NoError(t *testing.T) {
	if err := parseBrokerOptions(messaging.NewOptionsResolver()); err != nil {
		t.Fatalf("expected empty resolver to succeed, got: %v", err)
	}
	if err := parseBrokerOptions(nil); err != nil {
		t.Fatalf("expected nil resolver to succeed, got: %v", err)
	}
}
