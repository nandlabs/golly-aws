package s3

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/aws/smithy-go"
	"oss.nandlabs.io/golly/vfs"
)

// awsAPIErr is a synthetic APIError so we can drive mapS3Err without
// standing up a real S3 client.
type awsAPIErr struct {
	code    string
	message string
}

func (e *awsAPIErr) Error() string        { return e.code + ": " + e.message }
func (e *awsAPIErr) ErrorCode() string    { return e.code }
func (e *awsAPIErr) ErrorMessage() string { return e.message }
func (e *awsAPIErr) ErrorFault() smithy.ErrorFault {
	return smithy.FaultServer
}

func TestMapS3Err_Nil(t *testing.T) {
	if err := mapS3Err(nil); err != nil {
		t.Errorf("mapS3Err(nil) = %v, want nil", err)
	}
}

func TestMapS3Err_NoSuchKey_MapsToNotExist(t *testing.T) {
	src := &awsAPIErr{code: "NoSuchKey", message: "The specified key does not exist."}
	got := mapS3Err(src)
	if !errors.Is(got, vfs.ErrNotExist) {
		t.Errorf("NoSuchKey did not map to vfs.ErrNotExist: %v", got)
	}
	// vfs.ErrNotExist wraps fs.ErrNotExist, so stdlib checks still work
	if !errors.Is(got, fs.ErrNotExist) {
		t.Error("mapped error should still satisfy fs.ErrNotExist")
	}
	// original error is preserved in the chain
	var apiErr smithy.APIError
	if !errors.As(got, &apiErr) {
		t.Error("wrapped error should still be an APIError")
	}
}

func TestMapS3Err_NoSuchBucket_MapsToNotExist(t *testing.T) {
	src := &awsAPIErr{code: "NoSuchBucket"}
	if !errors.Is(mapS3Err(src), vfs.ErrNotExist) {
		t.Error("NoSuchBucket did not map to vfs.ErrNotExist")
	}
}

func TestMapS3Err_AccessDenied_MapsToPermission(t *testing.T) {
	src := &awsAPIErr{code: "AccessDenied"}
	got := mapS3Err(src)
	if !errors.Is(got, vfs.ErrPermission) {
		t.Errorf("AccessDenied did not map to vfs.ErrPermission: %v", got)
	}
	if !errors.Is(got, fs.ErrPermission) {
		t.Error("mapped error should still satisfy fs.ErrPermission")
	}
}

func TestMapS3Err_SignatureError_MapsToPermission(t *testing.T) {
	for _, code := range []string{"SignatureDoesNotMatch", "InvalidAccessKeyId", "AllAccessDisabled"} {
		if !errors.Is(mapS3Err(&awsAPIErr{code: code}), vfs.ErrPermission) {
			t.Errorf("%s did not map to vfs.ErrPermission", code)
		}
	}
}

func TestMapS3Err_UnknownCode_PassthroughUnchanged(t *testing.T) {
	src := &awsAPIErr{code: "SomeWeirdInternalError"}
	got := mapS3Err(src)
	if errors.Is(got, vfs.ErrNotExist) || errors.Is(got, vfs.ErrPermission) {
		t.Errorf("unknown error should not map to a sentinel: %v", got)
	}
	if !errors.Is(got, src) {
		t.Error("unknown error should be returned unchanged (same instance)")
	}
}
