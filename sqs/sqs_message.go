package sqs

import (
	"oss.nandlabs.io/golly/messaging"
)

type MessageSQS struct {
	*messaging.BaseMessage
}

func (lm *MessageSQS) Rsvp(yes bool, options ...messaging.Option) (err error) {
	return
}
