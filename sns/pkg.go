package sns

import (
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"oss.nandlabs.io/golly-aws/provider"
	"oss.nandlabs.io/golly/l3"
	"oss.nandlabs.io/golly/messaging"
)

var (
	logger             = l3.Get()
	sessionProviderMap = make(map[string]provider.ConfigProvider)
)

func init() {
	providerSNS := &ProviderSNS{}
	messagingManager := messaging.GetManager()
	messagingManager.Register(providerSNS)
}

func GetSession(region string) (config *aws.Config, err error) {
	var p provider.ConfigProvider
	var isRegistered bool
	if p, isRegistered = sessionProviderMap[region]; !isRegistered {
		p = provider.GetDefault()
	}
	if p != nil {
		config, err = p.Get()
	} else {
		err = errors.New("no session provider available for region and bucket")
	}
	return
}

func AddSessionProvider(region string, provider provider.ConfigProvider) {
	sessionProviderMap[region] = provider
}
