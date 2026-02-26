package sns

import "oss.nandlabs.io/golly/messaging"

// MessageSNS wraps BaseMessage for SNS. Since SNS is publish-only,
// Rsvp is a no-op that always returns nil.
type MessageSNS struct {
	*messaging.BaseMessage
	// messageId is the SNS message ID returned after publishing (populated after Send).
	messageId string
	// provider is a back-reference to the provider.
	provider *Provider
}

// Rsvp is a no-op for SNS since there is no acknowledgement concept.
// Always returns nil.
func (m *MessageSNS) Rsvp(_ bool, _ ...messaging.Option) error {
	return nil
}

// SNSMessageId returns the SNS-assigned message ID after the message has been published.
// Returns an empty string if the message has not been published yet.
func (m *MessageSNS) SNSMessageId() string {
	return m.messageId
}
