package s3

import (
	"errors"
	"net/url"
	"strings"
)

const (
	// S3Scheme is the URL scheme for S3 resources.
	S3Scheme = "s3"
)

// urlOpts holds parsed S3 URL components.
type urlOpts struct {
	u      *url.URL
	Bucket string
	Key    string
}

// parseURL parses an S3 URL into its bucket and key components.
// Expected format: s3://bucket-name/key/path
func parseURL(u *url.URL) (opts *urlOpts, err error) {
	if err = validateURL(u); err != nil {
		return
	}

	bucket := u.Host
	key := strings.TrimPrefix(u.Path, "/")

	opts = &urlOpts{
		u:      u,
		Bucket: bucket,
		Key:    key,
	}
	return
}

// validateURL checks that the URL is a valid S3 URL.
func validateURL(u *url.URL) error {
	if u == nil {
		return errors.New("url cannot be nil")
	}
	if u.Scheme != S3Scheme {
		return errors.New("invalid URL scheme, expected 's3'")
	}
	if u.Host == "" {
		return errors.New("invalid S3 URL, bucket name (host) is required")
	}
	return nil
}
