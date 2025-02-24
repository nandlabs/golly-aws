package s3vfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"oss.nandlabs.io/golly/textutils"
	"oss.nandlabs.io/golly/vfs"
)

type S3File struct {
	*vfs.BaseFile
	urlOpts *UrlOpts
	fs      vfs.VFileSystem
	closers []io.Closer
	client  *s3.Client
}

// Read - s3Object read the body
func (s3File *S3File) Read(b []byte) (body int, err error) {
	var result *s3.GetObjectOutput
	result, err = getS3Object(s3File)
	s3File.closers = append(s3File.closers, result.Body)
	defer s3File.Close()
	return result.Body.Read(b)
}

func (s3File *S3File) Write(b []byte) (numBytes int, err error) {
	// if key exists in s3 then the key will be overwritten else the new key with input body is created
	_, err = s3File.client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(s3File.urlOpts.Bucket),
		Key:    aws.String(s3File.urlOpts.Key),
		Body:   bytes.NewReader(b),
	})
	if err != nil {
		fmt.Println("Error writing file:", err)
		numBytes = 0
		return
	}
	numBytes = len(b)
	return
}

func (s3File *S3File) ListAll() (files []vfs.VFile, err error) {
	var result *s3.ListObjectsV2Output
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(s3File.urlOpts.Bucket),
		Prefix: aws.String(s3File.urlOpts.Key),
	}
	ctx := context.Background()
	var contents []types.Object
	objectPaginator := s3.NewListObjectsV2Paginator(s3File.client, input)
	for objectPaginator.HasMorePages() {
		result, err = objectPaginator.NextPage(ctx)
		if err != nil {
			return
		} else {
			contents = append(contents, result.Contents...)
		}
	}
	for _, item := range contents {
		var vFile vfs.VFile
		if s3File.urlOpts.Key != "" && (*item.Key == s3File.urlOpts.Key+textutils.ForwardSlashStr || *item.Key == s3File.urlOpts.Key) {
			continue
		}
		u := &url.URL{
			Scheme: s3File.urlOpts.u.Scheme,
			Host:   s3File.urlOpts.u.Host,
			Path:   *item.Key,
		}
		vFile, err = s3File.fs.Open(u)
		files = append(files, vFile)
	}
	return
}

func (s3File *S3File) Info() (file vfs.VFileInfo, err error) {
	result, err := s3File.client.HeadObject(context.TODO(), &s3.HeadObjectInput{
		Bucket: aws.String(s3File.urlOpts.Bucket),
		Key:    aws.String(s3File.urlOpts.Key),
	})
	file = &S3FileInfo{
		fs:           s3File.fs,
		key:          s3File.urlOpts.Key,
		size:         *result.ContentLength,
		lastModified: *result.LastModified,
	}
	return
}

func (s3File *S3File) AddProperty(name, value string) (err error) {
	// Create an input object for the CopyObject API operation.
	copyInput := &s3.CopyObjectInput{
		Bucket:            aws.String(s3File.urlOpts.Bucket),
		CopySource:        aws.String(fmt.Sprintf("%s/%s", s3File.urlOpts.Bucket, s3File.urlOpts.Key)),
		Key:               aws.String(s3File.urlOpts.Key),
		MetadataDirective: types.MetadataDirectiveReplace,
		Metadata: map[string]string{
			name: value,
		},
	}
	// Call the CopyObject API operation to create a copy of the object with the new metadata.
	_, err = s3File.client.CopyObject(context.Background(), copyInput)
	if err != nil {
		return
	}
	return
}

func (s3File *S3File) GetProperty(name string) (value string, err error) {
	var result *s3.HeadObjectOutput
	// Create an input object for the HeadObject API operation.
	input := &s3.HeadObjectInput{
		Bucket: aws.String(s3File.urlOpts.Bucket),
		Key:    aws.String(s3File.urlOpts.Key),
	}
	// Call the HeadObject API operation to retrieve the object metadata.
	result, err = s3File.client.HeadObject(context.Background(), input)
	if err != nil {
		return
	}
	value = result.Metadata[name]
	return
}

func (s3File *S3File) Url() *url.URL {
	return s3File.urlOpts.u
}

func (s3File *S3File) Delete() (err error) {
	input := &s3.DeleteObjectInput{
		Bucket: aws.String(s3File.urlOpts.Bucket),
		Key:    aws.String(s3File.urlOpts.Key),
	}
	_, err = s3File.client.DeleteObject(context.Background(), input)
	if err != nil {
		return
	}
	return
}

func (s3File *S3File) Close() (err error) {
	if len(s3File.closers) > 0 {
		for _, closable := range s3File.closers {
			err = closable.Close()
		}
	}
	return
}

func (s3File *S3File) String() string {
	return s3File.urlOpts.u.String()
}
