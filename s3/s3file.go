package s3

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"oss.nandlabs.io/golly/textutils"
	"oss.nandlabs.io/golly/vfs"
)

// S3File implements the vfs.VFile interface for S3 objects.
type S3File struct {
	*vfs.BaseFile
	client  *awss3.Client
	fs      *S3FS
	urlOpts *urlOpts
	// reader/writer state
	reader      io.ReadCloser
	writeBuffer *bytes.Buffer
	offset      int64
	contentType string
}

// Read reads from the S3 object.
func (f *S3File) Read(b []byte) (n int, err error) {
	if f.reader == nil {
		input := &awss3.GetObjectInput{
			Bucket: aws.String(f.urlOpts.Bucket),
			Key:    aws.String(f.urlOpts.Key),
		}
		result, getErr := f.client.GetObject(context.Background(), input)
		if getErr != nil {
			return 0, getErr
		}
		f.reader = result.Body
		if result.ContentType != nil {
			f.contentType = *result.ContentType
		}
	}
	n, err = f.reader.Read(b)
	f.offset += int64(n)
	return
}

// Write writes data to a buffer. The data is flushed to S3 on Close.
func (f *S3File) Write(b []byte) (n int, err error) {
	if f.writeBuffer == nil {
		f.writeBuffer = &bytes.Buffer{}
	}
	n, err = f.writeBuffer.Write(b)
	f.offset += int64(n)
	return
}

// Seek sets the offset for the next Read or Write. Only io.SeekStart with offset 0 resets the reader.
func (f *S3File) Seek(offset int64, whence int) (int64, error) {
	if whence == io.SeekStart && offset == 0 {
		// Reset reader so next Read starts from the beginning
		if f.reader != nil {
			_ = f.reader.Close()
			f.reader = nil
		}
		f.offset = 0
		return 0, nil
	}
	return f.offset, fmt.Errorf("seek not fully supported on S3 objects")
}

// Close flushes any buffered writes to S3 and closes open readers.
func (f *S3File) Close() error {
	var err error
	// Flush write buffer to S3
	if f.writeBuffer != nil && f.writeBuffer.Len() > 0 {
		ct := f.contentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		input := &awss3.PutObjectInput{
			Bucket:      aws.String(f.urlOpts.Bucket),
			Key:         aws.String(f.urlOpts.Key),
			Body:        f.writeBuffer,
			ContentType: aws.String(ct),
		}
		_, err = f.client.PutObject(context.Background(), input)
		f.writeBuffer = nil
	}
	// Close reader
	if f.reader != nil {
		closeErr := f.reader.Close()
		if err == nil {
			err = closeErr
		}
		f.reader = nil
	}
	return err
}

// ListAll lists all objects under this S3 prefix.
func (f *S3File) ListAll() (files []vfs.VFile, err error) {
	prefix := f.urlOpts.Key
	if prefix != "" && !strings.HasSuffix(prefix, textutils.ForwardSlashStr) {
		prefix = prefix + textutils.ForwardSlashStr
	}

	input := &awss3.ListObjectsV2Input{
		Bucket: aws.String(f.urlOpts.Bucket),
		Prefix: aws.String(prefix),
	}

	paginator := awss3.NewListObjectsV2Paginator(f.client, input)
	for paginator.HasMorePages() {
		page, pageErr := paginator.NextPage(context.Background())
		if pageErr != nil {
			return nil, pageErr
		}

		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			// Skip self
			if key == prefix || key == f.urlOpts.Key {
				continue
			}
			u := &url.URL{
				Scheme: S3Scheme,
				Host:   f.urlOpts.Bucket,
				Path:   "/" + key,
			}
			child, openErr := f.fs.Open(u)
			if openErr != nil {
				return nil, openErr
			}
			files = append(files, child)
		}

		// Also include common prefixes (virtual directories)
		for _, cp := range page.CommonPrefixes {
			cpKey := aws.ToString(cp.Prefix)
			u := &url.URL{
				Scheme: S3Scheme,
				Host:   f.urlOpts.Bucket,
				Path:   "/" + cpKey,
			}
			child, openErr := f.fs.Open(u)
			if openErr != nil {
				return nil, openErr
			}
			files = append(files, child)
		}
	}
	return
}

// Delete deletes the S3 object.
func (f *S3File) Delete() error {
	input := &awss3.DeleteObjectInput{
		Bucket: aws.String(f.urlOpts.Bucket),
		Key:    aws.String(f.urlOpts.Key),
	}
	_, err := f.client.DeleteObject(context.Background(), input)
	return err
}

// DeleteAll deletes all objects under this prefix (for directory-like objects).
func (f *S3File) DeleteAll() error {
	children, err := f.ListAll()
	if err != nil {
		return err
	}
	for _, child := range children {
		childInfo, infoErr := child.Info()
		if infoErr != nil {
			return infoErr
		}
		if childInfo.IsDir() {
			if delErr := child.DeleteAll(); delErr != nil {
				return delErr
			}
		} else {
			if delErr := child.Delete(); delErr != nil {
				return delErr
			}
		}
	}
	// Delete the prefix marker itself
	return f.Delete()
}

// Info returns the VFileInfo for this S3 object.
func (f *S3File) Info() (vfs.VFileInfo, error) {
	// Check if this is a "directory" (prefix ending with /)
	if strings.HasSuffix(f.urlOpts.Key, textutils.ForwardSlashStr) || f.urlOpts.Key == "" {
		return &S3FileInfo{
			fs:    f.fs,
			isDir: true,
			key:   f.urlOpts.Key,
		}, nil
	}

	input := &awss3.HeadObjectInput{
		Bucket: aws.String(f.urlOpts.Bucket),
		Key:    aws.String(f.urlOpts.Key),
	}
	result, err := f.client.HeadObject(context.Background(), input)
	if err != nil {
		// If HeadObject fails, check if it's a prefix (directory)
		listInput := &awss3.ListObjectsV2Input{
			Bucket:  aws.String(f.urlOpts.Bucket),
			Prefix:  aws.String(f.urlOpts.Key + textutils.ForwardSlashStr),
			MaxKeys: aws.Int32(1),
		}
		listResult, listErr := f.client.ListObjectsV2(context.Background(), listInput)
		if listErr != nil {
			return nil, err
		}
		if aws.ToInt32(listResult.KeyCount) > 0 {
			return &S3FileInfo{
				fs:    f.fs,
				isDir: true,
				key:   f.urlOpts.Key,
			}, nil
		}
		return nil, err
	}

	ct := ""
	if result.ContentType != nil {
		ct = *result.ContentType
	}

	return &S3FileInfo{
		fs:           f.fs,
		isDir:        false,
		key:          f.urlOpts.Key,
		lastModified: aws.ToTime(result.LastModified),
		size:         aws.ToInt64(result.ContentLength),
		contentType:  ct,
	}, nil
}

// Parent returns the parent directory of this file.
func (f *S3File) Parent() (vfs.VFile, error) {
	key := strings.TrimSuffix(f.urlOpts.Key, textutils.ForwardSlashStr)
	idx := strings.LastIndex(key, textutils.ForwardSlashStr)
	parentKey := ""
	if idx > 0 {
		parentKey = key[:idx+1]
	}
	u := &url.URL{
		Scheme: S3Scheme,
		Host:   f.urlOpts.Bucket,
		Path:   "/" + parentKey,
	}
	return f.fs.Open(u)
}

// Url returns the URL of this file.
func (f *S3File) Url() *url.URL {
	return f.urlOpts.u
}

// ContentType returns the content type of the S3 object.
func (f *S3File) ContentType() string {
	if f.contentType != "" {
		return f.contentType
	}
	return "application/octet-stream"
}

// AddProperty adds metadata to the S3 object using CopyObject with metadata replacement.
func (f *S3File) AddProperty(name, value string) error {
	ctx := context.Background()

	// Get current metadata
	headInput := &awss3.HeadObjectInput{
		Bucket: aws.String(f.urlOpts.Bucket),
		Key:    aws.String(f.urlOpts.Key),
	}
	headResult, err := f.client.HeadObject(ctx, headInput)
	if err != nil {
		return fmt.Errorf("failed to get object metadata: %w", err)
	}

	metadata := headResult.Metadata
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata[name] = value

	copySource := f.urlOpts.Bucket + "/" + f.urlOpts.Key
	copyInput := &awss3.CopyObjectInput{
		Bucket:            aws.String(f.urlOpts.Bucket),
		Key:               aws.String(f.urlOpts.Key),
		CopySource:        aws.String(copySource),
		Metadata:          metadata,
		MetadataDirective: "REPLACE",
	}
	_, err = f.client.CopyObject(ctx, copyInput)
	if err != nil {
		return fmt.Errorf("failed to update object metadata: %w", err)
	}

	logger.InfoF("Added metadata %q=%q to s3://%s/%s", name, value, f.urlOpts.Bucket, f.urlOpts.Key)
	return nil
}

// GetProperty retrieves a metadata value from the S3 object.
func (f *S3File) GetProperty(name string) (string, error) {
	input := &awss3.HeadObjectInput{
		Bucket: aws.String(f.urlOpts.Bucket),
		Key:    aws.String(f.urlOpts.Key),
	}
	result, err := f.client.HeadObject(context.Background(), input)
	if err != nil {
		return "", fmt.Errorf("failed to get object metadata: %w", err)
	}

	if val, ok := result.Metadata[name]; ok {
		return val, nil
	}
	return "", fmt.Errorf("metadata key %q not found", name)
}

// String returns the URL string of the file.
func (f *S3File) String() string {
	return f.urlOpts.u.String()
}
