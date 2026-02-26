package s3

import (
	"fmt"
	"os"
	"time"

	"oss.nandlabs.io/golly/vfs"
)

// S3FileInfo implements the vfs.VFileInfo interface for S3 objects.
type S3FileInfo struct {
	fs           vfs.VFileSystem
	isDir        bool
	key          string
	lastModified time.Time
	size         int64
	contentType  string
}

// Name returns the object key.
func (f *S3FileInfo) Name() string {
	return f.key
}

// Size returns the size of the object in bytes.
func (f *S3FileInfo) Size() int64 {
	return f.size
}

// Mode returns the file mode bits. Not applicable for S3, returns 0.
func (f *S3FileInfo) Mode() os.FileMode {
	return 0
}

// ModTime returns the last modified time of the object.
func (f *S3FileInfo) ModTime() time.Time {
	return f.lastModified
}

// IsDir returns true if the S3 object represents a directory (prefix).
func (f *S3FileInfo) IsDir() bool {
	return f.isDir
}

// Sys returns the underlying VFileSystem.
func (f *S3FileInfo) Sys() interface{} {
	return f.fs
}

// String returns a string representation of the file info.
func (f *S3FileInfo) String() string {
	return fmt.Sprintf("S3FileInfo{Name: %s, Size: %d, ModTime: %v, IsDir: %t}", f.key, f.size, f.lastModified, f.isDir)
}
