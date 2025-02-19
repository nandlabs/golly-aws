package s3vfs

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"oss.nandlabs.io/golly-aws/awssvc"
	"oss.nandlabs.io/golly/textutils"
)

type UrlOpts struct {
	u      *url.URL
	Host   string
	Bucket string
	Key    string
}

func (urlOpts *UrlOpts) String() string {
	return urlOpts.u.String()
}

// S3 url contains a region in itself
func parseUrl(url *url.URL) (*UrlOpts, error) {
	err := validateUrl(url)
	if err != nil {
		return nil, err
	}
	host := url.Host
	path := strings.TrimPrefix(url.String(), "s3://")
	components := strings.Split(path, "/")

	bucketName := components[0]
	objectPath := strings.Join(components[1:], "/")
	return &UrlOpts{
		u:      url,
		Host:   host,
		Bucket: bucketName,
		Key:    objectPath,
	}, nil
}

func validateUrl(u *url.URL) (err error) {
	storageUrl := u.String()
	if !strings.HasPrefix(storageUrl, "s3://") {
		return errors.New("invalid URL format, must start with 'storage://'")
	}
	path := strings.TrimPrefix(storageUrl, "s3://")
	components := strings.Split(path, "/")
	if len(components) < 1 {
		return errors.New("invalid URL, must specify at least a bucket name")
	}
	return
}

func (urlOpts *UrlOpts) CreateS3Client() (client *s3.Client, err error) {
	awsConfig := awssvc.Manager.Get(awssvc.ExtractKey(urlOpts.u))
	if awsConfig.Region == textutils.EmptyStr {
		awsConfig = awssvc.Manager.Get(urlOpts.Host)
		if awsConfig.Region == textutils.EmptyStr {
			awsConfig = awssvc.Manager.Get("s3")
			if awsConfig.Region == textutils.EmptyStr {
				awsConfig, err = config.LoadDefaultConfig(context.TODO())
				if err != nil {
					return
				}
			}
		}
	}
	client = s3.NewFromConfig(awsConfig)
	return
}

func keyExists(bucket, key string, client *s3.Client) (bool, error) {
	output, err := client.ListObjectsV2(context.TODO(), &s3.ListObjectsV2Input{
		Bucket:  aws.String(bucket),
		Prefix:  aws.String(key),
		MaxKeys: aws.Int32(1),
	})
	if err != nil {
		return false, err
	}
	return len(output.Contents) > 0, nil
}

func getS3Object(s3File *S3File) (result *s3.GetObjectOutput, err error) {
	result, err = s3File.client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(s3File.urlOpts.Bucket),
		Key:    aws.String(s3File.urlOpts.Key),
	})
	return
}
