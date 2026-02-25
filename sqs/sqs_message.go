package sqs

import "oss.nandlabs.io/golly/messaging"

// MessageSQS wraps BaseMessage and adds SQS-specific fields for Rsvp (acknowledgement).
type MessageSQS struct {
	*messaging.BaseMessage
	// receiptHandle is needed to delete (acknowledge) the message from SQS.
	receiptHandle string
	// queueURL is the SQS queue URL for acknowledgement.
	queueURL string
	// provider is a back-reference used for Rsvp.
	provider *Provider
}

// Rsvp acknowledges (deletes) or rejects the message.
// If accept is true, the message is deleted from the queue.
// If accept is false, the message visibility timeout is changed to 0 so it becomes immediately available for reprocessing.
func (m *MessageSQS) Rsvp(accept bool, options ...messaging.Option) (err error) {
	if m.provider == nil {
		return nil
	}
	if accept {
		err = m.provider.deleteMessage(m.queueURL, m.receiptHandle)
	} else {
		err = m.provider.changeVisibility(m.queueURL, m.receiptHandle, 0)
	}
	return
}
