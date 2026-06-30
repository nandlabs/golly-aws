package s3

import (
	"context"
	"io"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"oss.nandlabs.io/golly/textutils"
	"oss.nandlabs.io/golly/vfs"
)

// Compile-time check that S3FS implements the optional Lister capability
// so vfs.ListIter dispatches through us rather than falling back to the
// eager List slice.
var _ vfs.Lister = (*S3FS)(nil)

// ListIter returns a paginated FileIterator over the S3 prefix at u.
// Each Next() call advances within (or across) ListObjectsV2 pages; the
// next page is fetched lazily, so a million-key prefix never lands in a
// single slice.
func (fs *S3FS) ListIter(ctx context.Context, u *url.URL) (vfs.FileIterator, error) {
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
		prefix += textutils.ForwardSlashStr
	}
	paginator := awss3.NewListObjectsV2Paginator(client, &awss3.ListObjectsV2Input{
		Bucket:    aws.String(opts.Bucket),
		Prefix:    aws.String(prefix),
		Delimiter: aws.String(textutils.ForwardSlashStr),
	})
	return &s3FileIterator{
		fs:        fs,
		bucket:    opts.Bucket,
		prefix:    prefix,
		client:    client,
		paginator: paginator,
	}, nil
}

// s3FileIterator yields VFiles one at a time from ListObjectsV2 pages.
// Not safe for concurrent use.
type s3FileIterator struct {
	fs        *S3FS
	bucket    string
	prefix    string
	client    *awss3.Client
	paginator *awss3.ListObjectsV2Paginator

	// Buffer of pending VFiles from the current page (Contents + CommonPrefixes).
	buf  []vfs.VFile
	done bool
}

// Next returns the next VFile in the prefix, fetching the next page lazily
// when the current buffer is drained. Returns io.EOF when exhausted.
func (it *s3FileIterator) Next(ctx context.Context) (vfs.VFile, error) {
	if it.done {
		return nil, io.EOF
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for len(it.buf) == 0 {
		if !it.paginator.HasMorePages() {
			it.done = true
			return nil, io.EOF
		}
		page, err := it.paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			if key == it.prefix {
				continue
			}
			child, openErr := it.fs.Open(&url.URL{
				Scheme: S3Scheme, Host: it.bucket, Path: "/" + key,
			})
			if openErr != nil {
				return nil, openErr
			}
			it.buf = append(it.buf, child)
		}
		for _, cp := range page.CommonPrefixes {
			child, openErr := it.fs.Open(&url.URL{
				Scheme: S3Scheme, Host: it.bucket, Path: "/" + aws.ToString(cp.Prefix),
			})
			if openErr != nil {
				return nil, openErr
			}
			it.buf = append(it.buf, child)
		}
	}
	f := it.buf[0]
	it.buf = it.buf[1:]
	return f, nil
}

// Close discards any remaining buffered entries and stops further
// pagination. Idempotent; safe to call without consuming to EOF.
func (it *s3FileIterator) Close() error {
	it.done = true
	it.buf = nil
	return nil
}
