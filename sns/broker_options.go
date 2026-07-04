package sns

import (
	"fmt"

	"oss.nandlabs.io/golly/messaging"
)

// parseBrokerOptions extracts and validates the broker-targeted options
// defined by golly v1.6.0 (see oss.nandlabs.io/golly/messaging/broker_options.go)
// for SNS publish operations.
//
// SNS is a publish-only fan-out service, so most receive-side / redelivery-side
// options do not have a publisher-side equivalent:
//
//   - DeliveryGuarantee: AtLeastOnce is accepted (SNS default), AtMostOnce
//     is downgraded to a warning (SNS still delivers at-least-once), and
//     ExactlyOnce is rejected outright.
//   - DeadLetter: rejected — SNS DLQs are attached to a subscription's
//     RedrivePolicy, not set by the publisher.
//   - MaxDeliveryAttempts: rejected — retries are governed by the
//     subscription's delivery policy, not the publisher.
//   - ConsumerMode: rejected — SNS is publish-only.
//   - AckTimeout: rejected — no ack semantics on the publish side.
func parseBrokerOptions(optResolver *messaging.OptionsResolver) error {
	if optResolver == nil {
		return nil
	}

	if v, ok := optResolver.Get(messaging.DeliveryGuaranteeOpt); ok {
		g, ok := v.(messaging.DeliveryGuarantee)
		if !ok {
			return fmt.Errorf("sns: %s: expected messaging.DeliveryGuarantee, got %T", messaging.DeliveryGuaranteeOpt, v)
		}
		switch g {
		case messaging.AtLeastOnce:
			// SNS default — accept.
		case messaging.AtMostOnce:
			logger.WarnF("sns: DeliveryGuarantee=%q requested; SNS delivers at-least-once — messages may be redelivered by the broker", g)
		case messaging.ExactlyOnce:
			return fmt.Errorf("sns: DeliveryGuarantee=%q is not supported; SNS does not provide exactly-once semantics", g)
		default:
			return fmt.Errorf("sns: DeliveryGuarantee=%q is not recognized", g)
		}
	}

	if _, ok := optResolver.Get(messaging.DeadLetterOpt); ok {
		return fmt.Errorf("sns: DeadLetter is not settable via publisher options; SNS DLQs are configured on the subscription (RedrivePolicy on the SNS→SQS subscription)")
	}
	if _, ok := optResolver.Get(messaging.MaxDeliveryAttemptsOpt); ok {
		return fmt.Errorf("sns: MaxDeliveryAttempts is not settable via publisher options; retries are governed by the subscription's delivery policy")
	}
	if _, ok := optResolver.Get(messaging.ConsumerModeOpt); ok {
		return fmt.Errorf("sns: ConsumerMode is not applicable; SNS is publish-only (use SNS→SQS fan-out with the sqs package for consumer-side options)")
	}
	if _, ok := optResolver.Get(messaging.AckTimeoutOpt); ok {
		return fmt.Errorf("sns: AckTimeout is not applicable; SNS is publish-only")
	}

	return nil
}
