package sns

import "oss.nandlabs.io/golly/messaging"

type MessageSNS struct {
	*messaging.BaseMessage
}

func NewSNSMessage() *MessageSNS {
	return &MessageSNS{
		&messaging.BaseMessage{},
	}
}

func (lm *MessageSNS) Rsvp(yes bool, options ...messaging.Option) (err error) {
	return
}
