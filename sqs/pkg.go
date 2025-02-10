package sqs

import (
	"oss.nandlabs.io/golly/l3"
	"oss.nandlabs.io/golly/messaging"
)

var (
	logger = l3.Get()
)

func init() {
	providerSqs := &ProviderSQS{}
	messagingManager := messaging.GetManager()
	messagingManager.Register(providerSqs)
}
