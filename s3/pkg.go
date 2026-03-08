package s3

import (
	"context"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"oss.nandlabs.io/golly-aws/awscfg"
	"oss.nandlabs.io/golly/l3"
	"oss.nandlabs.io/golly/vfs"
)

var logger = l3.Get()

func init() {
	storageFs := &S3FS{}
	storageFs.BaseVFS = &vfs.BaseVFS{VFileSystem: storageFs}
	vfs.GetManager().Register(storageFs)
}

// getS3Client creates an S3 client using the awscfg config resolved for the given urlOpts.
func getS3Client(opts *urlOpts) (*awss3.Client, error) {
	cfg := awscfg.GetConfig(opts.u, S3Scheme)
	if cfg == nil {
		// Fallback: load default AWS config without awscfg registration
		awsCfg, err := (&awscfg.Config{}).LoadAWSConfig(context.Background())
		if err != nil {
			return nil, err
		}
		return awss3.NewFromConfig(awsCfg), nil
	}

	awsCfg, err := cfg.LoadAWSConfig(context.Background())
	if err != nil {
		return nil, err
	}

	var s3Opts []func(*awss3.Options)
	if cfg.Endpoint != "" {
		s3Opts = append(s3Opts, func(o *awss3.Options) {
			o.BaseEndpoint = &cfg.Endpoint
			o.UsePathStyle = true
		})
	}

	return awss3.NewFromConfig(awsCfg, s3Opts...), nil
}
