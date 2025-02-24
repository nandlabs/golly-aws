package s3vfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"oss.nandlabs.io/golly/textutils"
	"oss.nandlabs.io/golly/vfs"
)

const (
	fileScheme = "s3"
)

var localFsSchemes = []string{fileScheme}

type S3Fs struct {
	*vfs.BaseVFS
}

// Create : creating a file in the s3 bucket
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
	if err != nil {
		fmt.Printf("Error checking path: %v\n", err)
		return
	}
	if found {
		err = errors.New("object already exists")
		return
	}
	// create an empty file
	_, err = svc.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(urlOpts.Bucket),
		Key:    aws.String(urlOpts.Key),
		Body:   bytes.NewReader([]byte("")),
	})
	if err != nil {
		fmt.Println("Error uploading file:", err)
		return
	}
	file = &S3File{
		urlOpts: urlOpts,
		fs:      s3Fs,
		closers: make([]io.Closer, 0),
		client:  svc,
	}
	return
}

func (s3Fs *S3Fs) Mkdir(u *url.URL) (file vfs.VFile, err error) {
	return s3Fs.MkdirAll(u)
}

func (s3Fs *S3Fs) MkdirAll(u *url.URL) (file vfs.VFile, err error) {
	urlOpts, err := parseUrl(u)
	if err != nil {
		return
	}
	client, err := urlOpts.CreateS3Client()
	if err != nil {
		return
	}
	// check if the same path already exist on the s3 or not
	found, err := keyExists(urlOpts.Bucket, urlOpts.Key, client)
	if err != nil {
		fmt.Printf("Error checking path: %v\n", err)
		return
	}
	if found {
		err = errors.New("object already exists")
		return
	}
	path := urlOpts.Key
	if !strings.HasSuffix(path, textutils.ForwardSlashStr) {
		path = path + textutils.ForwardSlashStr
	}
	_, err = client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(urlOpts.Bucket),
		Key:    aws.String(path),
	})
	if err != nil {
		fmt.Println("Error uploading file:", err)
		return
	}
	file = &S3File{
		urlOpts: urlOpts,
		fs:      s3Fs,
		closers: make([]io.Closer, 0),
		client:  client,
	}
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
	// _, err = svc.GetObject(context.Background(), &s3.GetObjectInput{
	// 	Bucket: aws.String(urlOpts.Bucket),
	// 	Key:    aws.String(urlOpts.Key),
	// })
	// if err != nil {
	// 	fmt.Println("Error downloading file:", err)
	// 	return
	// }
	file = &S3File{
		fs:      s3Fs,
		client:  svc,
		urlOpts: urlOpts,
		closers: make([]io.Closer, 0),
	}
	return
}

func (s3Fs *S3Fs) Schemes() []string {
	return localFsSchemes
}
