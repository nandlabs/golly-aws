package s3

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"oss.nandlabs.io/golly/textutils"
	"oss.nandlabs.io/golly/vfs"
)

var fsSchemes = []string{S3Scheme}

// S3FS implements the vfs.VFileSystem interface for AWS S3.
type S3FS struct {
	*vfs.BaseVFS
}

// Schemes returns the URL schemes supported by this filesystem.
func (fs *S3FS) Schemes() []string {
	return fsSchemes
}

// Create creates a new empty object at the given S3 URL.
func (fs *S3FS) Create(u *url.URL) (vfs.VFile, error) {
	opts, err := parseURL(u)
	if err != nil {
		return nil, err
	}

	client, err := getS3Client(opts)
	if err != nil {
		return nil, err
	}

	// Check if object already exists
	headInput := &awss3.HeadObjectInput{
		Bucket: aws.String(opts.Bucket),
		Key:    aws.String(opts.Key),
	}
	_, headErr := client.HeadObject(context.Background(), headInput)
	if headErr == nil {
		return nil, fmt.Errorf("file s3://%s/%s already exists", opts.Bucket, opts.Key)
	}

	// Create empty object
	putInput := &awss3.PutObjectInput{
		Bucket: aws.String(opts.Bucket),
		Key:    aws.String(opts.Key),
		Body:   strings.NewReader(""),
	}
	_, err = client.PutObject(context.Background(), putInput)
	if err != nil {
		return nil, err
	}

	return newS3File(client, fs, opts), nil
}

// Mkdir creates a directory marker (key ending with /) in S3.
func (fs *S3FS) Mkdir(u *url.URL) (vfs.VFile, error) {
	return fs.MkdirAll(u)
}

// MkdirAll creates a directory marker in S3. Since S3 has no real directories,
// this creates a zero-byte object with a trailing slash.
func (fs *S3FS) MkdirAll(u *url.URL) (vfs.VFile, error) {
	opts, err := parseURL(u)
	if err != nil {
		return nil, err
	}

	client, err := getS3Client(opts)
	if err != nil {
		return nil, err
	}

	key := opts.Key
	if !strings.HasSuffix(key, textutils.ForwardSlashStr) {
		key = key + textutils.ForwardSlashStr
	}

	putInput := &awss3.PutObjectInput{
		Bucket: aws.String(opts.Bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader(""),
	}
	_, err = client.PutObject(context.Background(), putInput)
	if err != nil {
		return nil, err
	}

	// Update opts with directory key
	dirOpts := &urlOpts{
		u: &url.URL{
			Scheme: S3Scheme,
			Host:   opts.Bucket,
			Path:   "/" + key,
		},
		Bucket: opts.Bucket,
		Key:    key,
	}

	return newS3File(client, fs, dirOpts), nil
}

// Open opens an S3 object at the given URL. It does not validate existence.
func (fs *S3FS) Open(u *url.URL) (vfs.VFile, error) {
	opts, err := parseURL(u)
	if err != nil {
		return nil, err
	}

	client, err := getS3Client(opts)
	if err != nil {
		return nil, err
	}

	return newS3File(client, fs, opts), nil
}

// newS3File creates a new S3File instance.
func newS3File(client *awss3.Client, fs *S3FS, opts *urlOpts) *S3File {
	f := &S3File{
		client:  client,
		fs:      fs,
		urlOpts: opts,
	}
	f.BaseFile = &vfs.BaseFile{VFile: f}
	return f
}

// Copy copies an S3 object from src to dst. If src is a directory, copies all children recursively.
func (fs *S3FS) Copy(src, dst *url.URL) error {
	srcOpts, err := parseURL(src)
	if err != nil {
		return err
	}

	dstOpts, err := parseURL(dst)
	if err != nil {
		return err
	}

	client, err := getS3Client(srcOpts)
	if err != nil {
		return err
	}

	// Check if source is a "directory" (prefix)
	srcFile := newS3File(client, fs, srcOpts)
	srcInfo, err := srcFile.Info()
	if err != nil {
		// Not a directory, copy single object
		return fs.copySingleObject(client, srcOpts, dstOpts)
	}

	if !srcInfo.IsDir() {
		return fs.copySingleObject(client, srcOpts, dstOpts)
	}

	// Copy all children
	children, err := srcFile.ListAll()
	if err != nil {
		return err
	}

	for _, child := range children {
		childInfo, infoErr := child.Info()
		if infoErr != nil {
			return infoErr
		}
		childKey := strings.TrimPrefix(child.Url().Path, "/")
		relativePath := strings.TrimPrefix(childKey, srcOpts.Key)
		dstKey := dstOpts.Key + relativePath

		childDstURL := &url.URL{
			Scheme: S3Scheme,
			Host:   dstOpts.Bucket,
			Path:   "/" + dstKey,
		}

		if childInfo.IsDir() {
			if copyErr := fs.Copy(child.Url(), childDstURL); copyErr != nil {
				return copyErr
			}
		} else {
			childSrcOpts := &urlOpts{u: child.Url(), Bucket: srcOpts.Bucket, Key: childKey}
			childDstOpts := &urlOpts{u: childDstURL, Bucket: dstOpts.Bucket, Key: dstKey}
			if copyErr := fs.copySingleObject(client, childSrcOpts, childDstOpts); copyErr != nil {
				return copyErr
			}
		}
	}

	return nil
}

// copySingleObject copies a single S3 object using server-side copy if same region, otherwise streams.
func (fs *S3FS) copySingleObject(client *awss3.Client, src, dst *urlOpts) error {
	// Use S3 server-side copy
	copySource := src.Bucket + "/" + src.Key
	input := &awss3.CopyObjectInput{
		Bucket:     aws.String(dst.Bucket),
		Key:        aws.String(dst.Key),
		CopySource: aws.String(copySource),
	}
	_, err := client.CopyObject(context.Background(), input)
	if err != nil {
		// Fallback to stream copy (cross-region or cross-account)
		return fs.streamCopy(client, src, dst)
	}
	return nil
}

// streamCopy reads from source and writes to destination (for cross-region copies).
func (fs *S3FS) streamCopy(client *awss3.Client, src, dst *urlOpts) error {
	getInput := &awss3.GetObjectInput{
		Bucket: aws.String(src.Bucket),
		Key:    aws.String(src.Key),
	}
	getResult, err := client.GetObject(context.Background(), getInput)
	if err != nil {
		return err
	}
	defer func() {
		_ = getResult.Body.Close()
	}()

	putInput := &awss3.PutObjectInput{
		Bucket:      aws.String(dst.Bucket),
		Key:         aws.String(dst.Key),
		Body:        getResult.Body,
		ContentType: getResult.ContentType,
	}
	_, err = client.PutObject(context.Background(), putInput)
	return err
}

// Delete deletes the object at the given URL. If it's a directory, deletes all children.
func (fs *S3FS) Delete(src *url.URL) error {
	srcOpts, err := parseURL(src)
	if err != nil {
		return err
	}

	client, err := getS3Client(srcOpts)
	if err != nil {
		return err
	}

	srcFile := newS3File(client, fs, srcOpts)
	srcInfo, infoErr := srcFile.Info()

	if infoErr == nil && srcInfo.IsDir() {
		return srcFile.DeleteAll()
	}

	return srcFile.Delete()
}

// List lists all direct children of the given S3 prefix.
func (fs *S3FS) List(u *url.URL) ([]vfs.VFile, error) {
	opts, err := parseURL(u)
	if err != nil {
		return nil, err
	}

	client, err := getS3Client(opts)
	if err != nil {
		return nil, err
	}

	prefix := opts.Key
	if prefix != "" && !strings.HasSuffix(prefix, textutils.ForwardSlashStr) {
		prefix = prefix + textutils.ForwardSlashStr
	}

	input := &awss3.ListObjectsV2Input{
		Bucket:    aws.String(opts.Bucket),
		Prefix:    aws.String(prefix),
		Delimiter: aws.String(textutils.ForwardSlashStr),
	}

	var files []vfs.VFile
	paginator := awss3.NewListObjectsV2Paginator(client, input)
	for paginator.HasMorePages() {
		page, pageErr := paginator.NextPage(context.Background())
		if pageErr != nil {
			return nil, pageErr
		}

		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			if key == prefix {
				continue
			}
			childURL := &url.URL{
				Scheme: S3Scheme,
				Host:   opts.Bucket,
				Path:   "/" + key,
			}
			child, openErr := fs.Open(childURL)
			if openErr != nil {
				return nil, openErr
			}
			files = append(files, child)
		}

		for _, cp := range page.CommonPrefixes {
			cpKey := aws.ToString(cp.Prefix)
			childURL := &url.URL{
				Scheme: S3Scheme,
				Host:   opts.Bucket,
				Path:   "/" + cpKey,
			}
			child, openErr := fs.Open(childURL)
			if openErr != nil {
				return nil, openErr
			}
			files = append(files, child)
		}
	}

	return files, nil
}

// Walk traverses the S3 prefix tree recursively, calling fn for each file.
func (fs *S3FS) Walk(u *url.URL, fn vfs.WalkFn) error {
	opts, err := parseURL(u)
	if err != nil {
		return err
	}

	client, err := getS3Client(opts)
	if err != nil {
		return err
	}

	prefix := opts.Key
	if prefix != "" && !strings.HasSuffix(prefix, textutils.ForwardSlashStr) {
		prefix = prefix + textutils.ForwardSlashStr
	}

	input := &awss3.ListObjectsV2Input{
		Bucket: aws.String(opts.Bucket),
		Prefix: aws.String(prefix),
	}

	paginator := awss3.NewListObjectsV2Paginator(client, input)
	for paginator.HasMorePages() {
		page, pageErr := paginator.NextPage(context.Background())
		if pageErr != nil {
			return pageErr
		}

		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			if key == prefix {
				continue
			}
			childURL := &url.URL{
				Scheme: S3Scheme,
				Host:   opts.Bucket,
				Path:   "/" + key,
			}
			child, openErr := fs.Open(childURL)
			if openErr != nil {
				return openErr
			}
			if walkErr := fn(child); walkErr != nil {
				return walkErr
			}
		}
	}

	return nil
}

// Move moves an S3 object from src to dst (copy + delete).
func (fs *S3FS) Move(src, dst *url.URL) error {
	if err := fs.Copy(src, dst); err != nil {
		return err
	}
	return fs.Delete(src)
}

// Find finds files under the given location that match the filter.
func (fs *S3FS) Find(location *url.URL, filter vfs.FileFilter) ([]vfs.VFile, error) {
	var files []vfs.VFile
	err := fs.Walk(location, func(file vfs.VFile) error {
		pass, filterErr := filter(file)
		if filterErr != nil {
			return filterErr
		}
		if pass {
			files = append(files, file)
		}
		return nil
	})
	return files, err
}

// DeleteMatching deletes files that match the given filter.
func (fs *S3FS) DeleteMatching(location *url.URL, filter vfs.FileFilter) error {
	files, err := fs.Find(location, filter)
	if err != nil {
		return err
	}
	for _, file := range files {
		if delErr := file.Delete(); delErr != nil {
			return delErr
		}
	}
	return nil
}

// CopyRaw copies from src URL string to dst URL string.
func (fs *S3FS) CopyRaw(src, dst string) error {
	srcURL, err := url.Parse(src)
	if err != nil {
		return err
	}
	dstURL, err := url.Parse(dst)
	if err != nil {
		return err
	}
	return fs.Copy(srcURL, dstURL)
}

// CreateRaw creates a file at the given URL string.
func (fs *S3FS) CreateRaw(raw string) (vfs.VFile, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	return fs.Create(u)
}

// DeleteRaw deletes the object at the given URL string.
func (fs *S3FS) DeleteRaw(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	return fs.Delete(u)
}

// ListRaw lists objects at the given URL string.
func (fs *S3FS) ListRaw(raw string) ([]vfs.VFile, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	return fs.List(u)
}

// MkdirRaw creates a directory at the given URL string.
func (fs *S3FS) MkdirRaw(raw string) (vfs.VFile, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	return fs.Mkdir(u)
}

// MkdirAllRaw creates a directory at the given URL string.
func (fs *S3FS) MkdirAllRaw(raw string) (vfs.VFile, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	return fs.MkdirAll(u)
}

// MoveRaw moves from src URL string to dst URL string.
func (fs *S3FS) MoveRaw(src, dst string) error {
	srcURL, err := url.Parse(src)
	if err != nil {
		return err
	}
	dstURL, err := url.Parse(dst)
	if err != nil {
		return err
	}
	return fs.Move(srcURL, dstURL)
}

// OpenRaw opens the S3 object at the given URL string.
func (fs *S3FS) OpenRaw(raw string) (vfs.VFile, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	return fs.Open(u)
}

// WalkRaw walks the S3 prefix tree at the given URL string.
func (fs *S3FS) WalkRaw(raw string, fn vfs.WalkFn) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	return fs.Walk(u, fn)
}
