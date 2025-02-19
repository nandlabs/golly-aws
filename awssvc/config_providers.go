package awssvc

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"oss.nandlabs.io/golly/l3"
	"oss.nandlabs.io/golly/managers"
)

var logger = l3.Get()

var Manager = managers.NewItemManager[aws.Config]()

func NewDefaultConfig() (aws.Config, error) {
	return config.LoadDefaultConfig(context.TODO())
}

func CustomRegionRegion(region string) (customCfg aws.Config, err error) {
	customCfg, err = config.LoadDefaultConfig(context.TODO(), config.WithRegion(region))
	if err != nil {
		logger.ErrorF("Failed to load custom configuration: %v", err)
		return
	}
	return
}
