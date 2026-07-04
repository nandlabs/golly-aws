package s3

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"oss.nandlabs.io/golly/textutils"
	"oss.nandlabs.io/golly/vfs"
)

// Compile-time check that S3FS satisfies vfs.VFileSystemCtx so callers can
// type-assert (or rely on vfs.OpenCtx/CopyCtx/... to dispatch automatically).
var _ vfs.VFileSystemCtx = (*S3FS)(nil)

// OpenCtx is the context-aware variant of Open. It does not validate
// existence; the ctx is forwarded to the underlying SDK call when the
// returned VFile reads or writes.
func (fs *S3FS) OpenCtx(_ context.Context, u *url.URL) (vfs.VFile, error) {
	// Open itself doesn't make a remote call — it just constructs a
	// reference. Reads/writes on the returned VFile pick up ctx from
	// their own call sites.
	return fs.Open(u)
}

// CreateCtx is the context-aware variant of Create.
func (fs *S3FS) CreateCtx(ctx context.Context, u *url.URL) (vfs.VFile, error) {
	opts, err := parseURL(u)
	if err != nil {
		return nil, err
	}
	client, err := getS3Client(opts)
	if err != nil {
		return nil, err
	}

	if _, headErr := client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(opts.Bucket),
		Key:    aws.String(opts.Key),
	}); headErr == nil {
		return nil, fmt.Errorf("file s3://%s/%s already exists", opts.Bucket, opts.Key)
	}

	if _, err = client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(opts.Bucket),
		Key:    aws.String(opts.Key),
		Body:   strings.NewReader(""),
	}); err != nil {
		return nil, mapS3Err(err)
	}
	return newS3File(client, fs, opts), nil
}

// MkdirAllCtx is the context-aware variant of MkdirAll.
func (fs *S3FS) MkdirAllCtx(ctx context.Context, u *url.URL) (vfs.VFile, error) {
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
		key += textutils.ForwardSlashStr
	}
	if _, err = client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(opts.Bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader(""),
	}); err != nil {
		return nil, mapS3Err(err)
	}
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

// DeleteCtx is the context-aware variant of Delete. ctx is propagated into
// the HeadObject probe; the underlying VFile.Delete path does not yet
// thread ctx so deletion of large directory trees still uses Background.
func (fs *S3FS) DeleteCtx(ctx context.Context, src *url.URL) error {
	srcOpts, err := parseURL(src)
	if err != nil {
		return err
	}
	client, err := getS3Client(srcOpts)
	if err != nil {
		return err
	}

	// Probe for directory-ness with ctx, then dispatch.
	_, headErr := client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(srcOpts.Bucket),
		Key:    aws.String(srcOpts.Key),
	})
	if headErr != nil {
		// May be a "directory" (no real object); fall through to delete-all
		// via existing VFile.DeleteAll path (it walks itself).
		return newS3File(client, fs, srcOpts).DeleteAll()
	}

	// Single-object delete with ctx.
	if _, err := client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(srcOpts.Bucket),
		Key:    aws.String(srcOpts.Key),
	}); err != nil {
		return mapS3Err(err)
	}
	return nil
}

// ListCtx is the context-aware variant of List.
func (fs *S3FS) ListCtx(ctx context.Context, u *url.URL) ([]vfs.VFile, error) {
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
	var files []vfs.VFile
	for paginator.HasMorePages() {
		page, pageErr := paginator.NextPage(ctx)
		if pageErr != nil {
			return nil, mapS3Err(pageErr)
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			if key == prefix {
				continue
			}
			child, openErr := fs.Open(&url.URL{
				Scheme: S3Scheme, Host: opts.Bucket, Path: "/" + key,
			})
			if openErr != nil {
				return nil, openErr
			}
			files = append(files, child)
		}
		for _, cp := range page.CommonPrefixes {
			child, openErr := fs.Open(&url.URL{
				Scheme: S3Scheme, Host: opts.Bucket, Path: "/" + aws.ToString(cp.Prefix),
			})
			if openErr != nil {
				return nil, openErr
			}
			files = append(files, child)
		}
	}
	return files, nil
}

// WalkCtx is the context-aware variant of Walk. ctx is checked between
// pages and inside fn (callers may also test ctx.Err() inside fn for
// finer cancellation granularity on long-running walks).
func (fs *S3FS) WalkCtx(ctx context.Context, u *url.URL, fn vfs.WalkFn) error {
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
		prefix += textutils.ForwardSlashStr
	}
	paginator := awss3.NewListObjectsV2Paginator(client, &awss3.ListObjectsV2Input{
		Bucket: aws.String(opts.Bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, pageErr := paginator.NextPage(ctx)
		if pageErr != nil {
			return mapS3Err(pageErr)
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			if key == prefix {
				continue
			}
			child, openErr := fs.Open(&url.URL{
				Scheme: S3Scheme, Host: opts.Bucket, Path: "/" + key,
			})
			if openErr != nil {
				return openErr
			}
			if walkErr := fn(child); walkErr != nil {
				if errors.Is(walkErr, vfs.ErrSkipAll) {
					return nil
				}
				if errors.Is(walkErr, vfs.ErrSkipDir) {
					// S3 has no real directories — at this level the
					// flat object listing has no children to skip, so
					// the sentinel behaves the same as continue.
					continue
				}
				return walkErr
			}
		}
	}
	return nil
}

// CopyCtx is the context-aware variant of Copy. For directory copies the
// recursive walk uses ctx; for single-object copies the underlying
// CopyObject / GetObject+PutObject calls receive ctx.
func (fs *S3FS) CopyCtx(ctx context.Context, src, dst *url.URL) error {
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

	srcFile := newS3File(client, fs, srcOpts)
	srcInfo, infoErr := srcFile.Info()
	if infoErr != nil || !srcInfo.IsDir() {
		return fs.copySingleObjectCtx(ctx, client, srcOpts, dstOpts)
	}

	children, err := srcFile.ListAll()
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return err
		}
		childInfo, infoErr := child.Info()
		if infoErr != nil {
			return infoErr
		}
		childKey := strings.TrimPrefix(child.Url().Path, "/")
		relativePath := strings.TrimPrefix(childKey, srcOpts.Key)
		dstKey := dstOpts.Key + relativePath
		childDstURL := &url.URL{Scheme: S3Scheme, Host: dstOpts.Bucket, Path: "/" + dstKey}
		if childInfo.IsDir() {
			if copyErr := fs.CopyCtx(ctx, child.Url(), childDstURL); copyErr != nil {
				return copyErr
			}
		} else {
			childSrcOpts := &urlOpts{u: child.Url(), Bucket: srcOpts.Bucket, Key: childKey}
			childDstOpts := &urlOpts{u: childDstURL, Bucket: dstOpts.Bucket, Key: dstKey}
			if copyErr := fs.copySingleObjectCtx(ctx, client, childSrcOpts, childDstOpts); copyErr != nil {
				return copyErr
			}
		}
	}
	return nil
}

func (fs *S3FS) copySingleObjectCtx(ctx context.Context, client *awss3.Client, src, dst *urlOpts) error {
	copySource := src.Bucket + "/" + src.Key
	_, err := client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket:     aws.String(dst.Bucket),
		Key:        aws.String(dst.Key),
		CopySource: aws.String(copySource),
	})
	if err == nil {
		return nil
	}
	// Fallback to stream copy (cross-region / cross-account).
	getResult, gerr := client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(src.Bucket), Key: aws.String(src.Key),
	})
	if gerr != nil {
		return gerr
	}
	defer func() { _ = getResult.Body.Close() }()
	_, perr := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:      aws.String(dst.Bucket),
		Key:         aws.String(dst.Key),
		Body:        getResult.Body,
		ContentType: getResult.ContentType,
	})
	return perr
}

// MoveCtx is CopyCtx + DeleteCtx.
func (fs *S3FS) MoveCtx(ctx context.Context, src, dst *url.URL) error {
	if err := fs.CopyCtx(ctx, src, dst); err != nil {
		return err
	}
	return fs.DeleteCtx(ctx, src)
}
