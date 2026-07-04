package sqs

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqsTypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"oss.nandlabs.io/golly/messaging"
)

// brokerOptions holds the AWS-translated pieces of the broker-targeted
// messaging options (see oss.nandlabs.io/golly/messaging/broker_options.go).
//
// Constructed by parseBrokerOptions from a messaging.OptionsResolver. Fields
// are only populated when the caller supplied the corresponding option;
// the has* booleans distinguish "unset" from "zero".
type brokerOptions struct {
	// visibilityTimeout, if set, is the SQS VisibilityTimeout (in seconds)
	// derived from AckTimeoutOpt (a time.Duration).
	visibilityTimeout    int32
	hasVisibilityTimeout bool

	// maxDeliveryAttempts, if set, is the caller's minimum acceptable
	// RedrivePolicy.maxReceiveCount on the target queue.
	maxDeliveryAttempts    int
	hasMaxDeliveryAttempts bool

	// deadLetter, if non-empty, is the DLQ identifier (queue name or ARN)
	// the caller expects the target queue's RedrivePolicy to route to.
	deadLetter string
}

// queueAttributesGetter is the subset of *sqs.Client used by the redrive-policy
// check. Extracted so tests can substitute a fake without spinning up a queue.
type queueAttributesGetter interface {
	GetQueueAttributes(ctx context.Context, params *sqs.GetQueueAttributesInput, optFns ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error)
}

// parseBrokerOptions extracts and validates the broker-targeted options
// defined by golly v1.6.0. isFIFO tells the parser whether the target queue
// is a FIFO queue (name ends in .fifo); this affects DeliveryGuarantee
// validation because ExactlyOnce is only accepted on FIFO queues, where it
// means dedup + strict FIFO order.
//
// The returned brokerOptions is safe to use even when no options were set
// (all fields will be zero / has* booleans false).
func parseBrokerOptions(optResolver *messaging.OptionsResolver, isFIFO bool) (*brokerOptions, error) {
	bo := &brokerOptions{}
	if optResolver == nil {
		return bo, nil
	}

	if v, ok := optResolver.Get(messaging.DeliveryGuaranteeOpt); ok {
		g, ok := v.(messaging.DeliveryGuarantee)
		if !ok {
			return nil, fmt.Errorf("sqs: %s: expected messaging.DeliveryGuarantee, got %T", messaging.DeliveryGuaranteeOpt, v)
		}
		switch g {
		case messaging.AtLeastOnce:
			// SQS (Standard + FIFO) both provide at-least-once — accept.
		case messaging.AtMostOnce:
			return nil, fmt.Errorf("sqs: DeliveryGuarantee=%q is not supported; SQS delivers at-least-once", g)
		case messaging.ExactlyOnce:
			if !isFIFO {
				return nil, fmt.Errorf("sqs: DeliveryGuarantee=%q requires a FIFO queue (queue name must end in .fifo) with content-based deduplication or an explicit MessageDeduplicationId", g)
			}
			// FIFO exactly-once = dedup + FIFO order — accept.
		default:
			return nil, fmt.Errorf("sqs: DeliveryGuarantee=%q is not recognized", g)
		}
	}

	if v, ok := optResolver.Get(messaging.ConsumerModeOpt); ok {
		m, ok := v.(messaging.ConsumerMode)
		if !ok {
			return nil, fmt.Errorf("sqs: %s: expected messaging.ConsumerMode, got %T", messaging.ConsumerModeOpt, v)
		}
		switch m {
		case messaging.ConsumerPull:
			// SQS long-polling is pull-style — accept.
		case messaging.ConsumerPush:
			return nil, fmt.Errorf("sqs: ConsumerMode=%q is not supported; SQS is pull-based (long-poll only)", m)
		default:
			return nil, fmt.Errorf("sqs: ConsumerMode=%q is not recognized", m)
		}
	}

	if v, ok := optResolver.Get(messaging.AckTimeoutOpt); ok {
		d, ok := v.(time.Duration)
		if !ok {
			return nil, fmt.Errorf("sqs: %s: expected time.Duration, got %T", messaging.AckTimeoutOpt, v)
		}
		if d <= 0 {
			return nil, fmt.Errorf("sqs: AckTimeout must be > 0, got %s", d)
		}
		secs := int64(d.Seconds())
		if secs < 1 {
			// SQS visibility timeout has 1s minimum granularity; round up.
			secs = 1
		}
		if secs > 43200 {
			return nil, fmt.Errorf("sqs: AckTimeout %s exceeds SQS VisibilityTimeout maximum of 12h (43200s)", d)
		}
		bo.visibilityTimeout = int32(secs)
		bo.hasVisibilityTimeout = true
	}

	if v, ok := optResolver.Get(messaging.MaxDeliveryAttemptsOpt); ok {
		n, ok := v.(int)
		if !ok {
			return nil, fmt.Errorf("sqs: %s: expected int, got %T", messaging.MaxDeliveryAttemptsOpt, v)
		}
		if n <= 0 {
			return nil, fmt.Errorf("sqs: MaxDeliveryAttempts must be > 0, got %d", n)
		}
		bo.maxDeliveryAttempts = n
		bo.hasMaxDeliveryAttempts = true
	}

	if v, ok := optResolver.Get(messaging.DeadLetterOpt); ok {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("sqs: %s: expected string (DLQ name or ARN), got %T", messaging.DeadLetterOpt, v)
		}
		if s == "" {
			return nil, fmt.Errorf("sqs: DeadLetter must be a non-empty queue name or ARN")
		}
		bo.deadLetter = s
	}

	return bo, nil
}

// validateRedrivePolicy fetches the target queue's RedrivePolicy and confirms
// it satisfies the caller's MaxDeliveryAttempts / DeadLetter requirements.
// It is invoked from AddListener / ReceiveBatch when either option is set.
//
// This provider deliberately does NOT create the DLQ or attach a redrive
// policy — DLQ topology is an infrastructure decision that belongs at queue
// creation time. If the queue lacks a RedrivePolicy, an error is returned
// pointing the caller at queue creation.
func (bo *brokerOptions) validateRedrivePolicy(ctx context.Context, client queueAttributesGetter, queueURL string) error {
	if !bo.hasMaxDeliveryAttempts && bo.deadLetter == "" {
		return nil
	}
	out, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: &queueURL,
		AttributeNames: []sqsTypes.QueueAttributeName{
			sqsTypes.QueueAttributeNameRedrivePolicy,
		},
	})
	if err != nil {
		return fmt.Errorf("sqs: failed to fetch RedrivePolicy for %s: %w", queueURL, err)
	}
	raw, ok := out.Attributes[string(sqsTypes.QueueAttributeNameRedrivePolicy)]
	if !ok || raw == "" {
		return fmt.Errorf("sqs: queue %s has no RedrivePolicy; configure a DLQ + maxReceiveCount at queue creation time (this provider does not auto-create DLQs)", queueURL)
	}
	var rp struct {
		DeadLetterTargetArn string `json:"deadLetterTargetArn"`
		MaxReceiveCount     any    `json:"maxReceiveCount"`
	}
	if err := json.Unmarshal([]byte(raw), &rp); err != nil {
		return fmt.Errorf("sqs: RedrivePolicy on %s is not valid JSON: %w", queueURL, err)
	}
	if bo.hasMaxDeliveryAttempts {
		got, err := redriveMaxReceiveCount(rp.MaxReceiveCount)
		if err != nil {
			return fmt.Errorf("sqs: RedrivePolicy.maxReceiveCount on %s: %w", queueURL, err)
		}
		if got < bo.maxDeliveryAttempts {
			return fmt.Errorf("sqs: queue %s RedrivePolicy.maxReceiveCount=%d is less than requested MaxDeliveryAttempts=%d", queueURL, got, bo.maxDeliveryAttempts)
		}
	}
	if bo.deadLetter != "" {
		if !dlqIdentifierMatches(rp.DeadLetterTargetArn, bo.deadLetter) {
			return fmt.Errorf("sqs: queue %s RedrivePolicy.deadLetterTargetArn=%q does not match requested DeadLetter=%q", queueURL, rp.DeadLetterTargetArn, bo.deadLetter)
		}
	}
	return nil
}

// redriveMaxReceiveCount coerces the maxReceiveCount from the parsed
// RedrivePolicy JSON. AWS returns it as a JSON number (float64 after
// unmarshal) but older tooling has been known to serialise it as a string,
// so we accept both.
func redriveMaxReceiveCount(v any) (int, error) {
	switch n := v.(type) {
	case float64:
		return int(n), nil
	case int:
		return n, nil
	case string:
		i, err := strconv.Atoi(n)
		if err != nil {
			return 0, fmt.Errorf("expected integer, got %q", n)
		}
		return i, nil
	default:
		return 0, fmt.Errorf("unexpected type %T", v)
	}
}

// dlqIdentifierMatches compares the queue's actual DLQ ARN against the
// caller's requested identifier. Accepts an exact match, or an ARN whose
// tail segment (the queue name) matches the caller's bare name.
func dlqIdentifierMatches(arn, want string) bool {
	if arn == want {
		return true
	}
	if idx := strings.LastIndex(arn, ":"); idx >= 0 && idx+1 < len(arn) {
		return arn[idx+1:] == want
	}
	return false
}

// isFIFOQueue reports whether a queue name or URL denotes a FIFO queue
// (the AWS convention is a .fifo suffix on the queue name).
func isFIFOQueue(nameOrURL string) bool {
	return strings.HasSuffix(nameOrURL, ".fifo")
}
