package awssvc

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"oss.nandlabs.io/golly/managers"
)

var Manager = managers.NewItemManager[aws.Config]()
