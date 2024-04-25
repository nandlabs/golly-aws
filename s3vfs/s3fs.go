package s3vfs

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"oss.nandlabs.io/golly/vfs"
)

const (
	fileScheme = "s3"
)

var localFsSchemes = []string{fileScheme}

type S3Fs struct {
	*vfs.BaseVFS
}

// Create : creating a file in the s3 bucket, can create both object and bucket
func (s3Fs *S3Fs) Create(u *url.URL) (file vfs.VFile, err error) {
	var urlOpts *UrlOpts
	var svc *s3.Client
	var found bool

	urlOpts, err = parseUrl(u)
	if err != nil {
		return
	}
	svc, err = urlOpts.CreateS3Client()
	if err != nil {
		return
	}
	// check if the same path already exist on the s3 or not
	found, err = keyExists(urlOpts.Bucket, urlOpts.Key, svc)
	if !found {
		return
	}

	// create the folder structure or an empty file
	_, err = svc.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(urlOpts.Bucket),
		Key:    aws.String(urlOpts.Key),
	})
	if err != nil {
		fmt.Println("Error uploading file:", err)
		return
	}
	return
}

func (s3Fs *S3Fs) Mkdir(u *url.URL) (file vfs.VFile, err error) {
	err = errors.New("operation Mkdir not supported")
	return
}

func (s3Fs *S3Fs) MkdirAll(u *url.URL) (file vfs.VFile, err error) {
	err = errors.New("operation MkdirAll not supported")
	return
}

// Open location provided of the S3 bucket
func (s3Fs *S3Fs) Open(u *url.URL) (file vfs.VFile, err error) {
	var urlOpts *UrlOpts
	var svc *s3.Client

	urlOpts, err = parseUrl(u)
	if err != nil {
		return
	}
	svc, err = urlOpts.CreateS3Client()
	if err != nil {
		return
	}

	_, err = svc.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(urlOpts.Bucket),
		Key:    aws.String(urlOpts.Key),
	})
	if err != nil {
		fmt.Println("Error downloading file:", err)
		return
	}
	if err == nil {
		file = &S3File{
			Location: u,
			fs:       s3Fs,
		}
	}
	return
}

func (s3Fs *S3Fs) Schemes() []string {
	return localFsSchemes
}
