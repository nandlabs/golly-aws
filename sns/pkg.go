package sns

import (
	"oss.nandlabs.io/golly/l3"
	"oss.nandlabs.io/golly/messaging"
)

var (
	logger = l3.Get()
)

func init() {
	providerSNS := &ProviderSNS{}
	messagingManager := messaging.GetManager()
	messagingManager.Register(providerSNS)
}
