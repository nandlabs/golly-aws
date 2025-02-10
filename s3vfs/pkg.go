package s3vfs

import (
	"oss.nandlabs.io/golly/l3"
	"oss.nandlabs.io/golly/vfs"
)

var (
	logger = l3.Get()
	// sessionProviderMap = make(map[string]provider.ConfigProvider)
)

func init() {
	s3Fs := &S3Fs{}
	vfs.GetManager().Register(s3Fs)
}

// GetSession function will retrieve the *aws.Config object for the region & Bucket combination
// func GetSession(region, bucket string) (config *aws.Config, err error) {
// 	var p provider.ConfigProvider
// 	var isRegistered bool
// 	if p, isRegistered = sessionProviderMap[region+textutils.ColonStr+bucket]; !isRegistered {
// 		p = provider.GetDefault()
// 	}
// 	if p != nil {
// 		config, err = p.Get()
// 	} else {
// 		err = errors.New("no session provider available for region and bucket")
// 	}
// 	return
// }

// func AddSessionProvider(region, bucket string, provider provider.ConfigProvider) {
// 	sessionProviderMap[region+textutils.ColonStr+bucket] = provider

// }
