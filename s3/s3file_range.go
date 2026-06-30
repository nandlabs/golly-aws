package s3

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"oss.nandlabs.io/golly/vfs"
)

// Compile-time check that S3File implements the optional vfs.RangeReader
// capability so vfs.ReadRange dispatches through us — translating the
// caller's [off, off+length) to an HTTP Range request rather than
// falling back to Seek+Read (which would download the whole object on
// many S3 implementations).
var _ vfs.RangeReader = (*S3File)(nil)

// ReadRange returns up to length bytes starting at off via an S3 ranged
// GET. A length of 0 means "read to EOF from off". Returns (nil, io.EOF)
// when off is past the end of the object.
//
// The single GetObject call here issues a single HTTP request with an
// HTTP Range header — only the requested bytes are transferred over
// the wire, no matter how large the underlying object is.
func (f *S3File) ReadRange(ctx context.Context, off, length int64) ([]byte, error) {
	if off < 0 {
		return nil, fmt.Errorf("s3: negative offset %d", off)
	}
	if length < 0 {
		return nil, fmt.Errorf("s3: negative length %d", length)
	}

	// Build the Range header. Per RFC 7233 the end is inclusive.
	//   length == 0  → "bytes=off-"   (off through EOF)
	//   length  > 0  → "bytes=off-end" (off through off+length-1)
	var rangeHeader string
	if length == 0 {
		rangeHeader = fmt.Sprintf("bytes=%d-", off)
	} else {
		rangeHeader = fmt.Sprintf("bytes=%d-%d", off, off+length-1)
	}

	resp, err := f.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(f.urlOpts.Bucket),
		Key:    aws.String(f.urlOpts.Key),
		Range:  aws.String(rangeHeader),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if length > 0 && int64(len(buf)) > length {
		buf = buf[:length]
	}
	if length > 0 && len(buf) == 0 {
		return nil, io.EOF
	}
	return buf, nil
}
