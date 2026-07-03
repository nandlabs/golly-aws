package s3

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"oss.nandlabs.io/golly/vfs"
)

// mapS3Err translates an AWS SDK v2 S3 error into a golly/vfs sentinel
// so callers can switch on outcomes with errors.Is across backends.
// It returns nil for a nil input, and returns the original error
// wrapped with a matching vfs sentinel when the AWS error code (or HTTP
// status) maps cleanly. If no mapping is known, the original error is
// returned unchanged so callers still see the SDK detail.
//
// Callers should wrap SDK errors at each callsite:
//
//	if _, err := client.GetObject(ctx, in); err != nil {
//	    return nil, mapS3Err(err)
//	}
//
// The returned error preserves the original chain (via fmt.Errorf %w),
// so errors.Is(err, vfs.ErrNotExist) AND the SDK-typed error assertion
// both continue to work.
func mapS3Err(err error) error {
	if err == nil {
		return nil
	}

	// Prefer the smithy APIError code — stable across AWS regions and
	// versions, unlike HTTP status which some backends fudge.
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NoSuchBucket", "NotFound":
			return fmt.Errorf("%w: %w", vfs.ErrNotExist, err)
		case "AccessDenied", "Forbidden", "AllAccessDisabled",
			"InvalidAccessKeyId", "SignatureDoesNotMatch":
			return fmt.Errorf("%w: %w", vfs.ErrPermission, err)
		}
	}

	// Fallback: some transient / regional errors surface only as
	// HTTP status via smithyhttp.ResponseError. Map 404/403 to the
	// same sentinels.
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) && respErr.Response != nil {
		switch respErr.Response.StatusCode {
		case http.StatusNotFound:
			return fmt.Errorf("%w: %w", vfs.ErrNotExist, err)
		case http.StatusForbidden, http.StatusUnauthorized:
			return fmt.Errorf("%w: %w", vfs.ErrPermission, err)
		}
	}

	return err
}
