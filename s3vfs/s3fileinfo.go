package s3vfs

import (
	"fmt"
	"os"
	"time"

	"oss.nandlabs.io/golly/vfs"
)

type S3FileInfo struct {
	fs           vfs.VFileSystem
	isDir        bool
	key          string
	size         int64
	lastModified time.Time
}

func (f *S3FileInfo) Name() string {
	return f.key
}

func (f *S3FileInfo) Size() int64 {
	return f.size
}

func (f *S3FileInfo) Mode() os.FileMode {
	// Not applicable for S3 Objects, return default value
	return 0
}
func (f *S3FileInfo) ModTime() time.Time {
	return f.lastModified
}

func (f *S3FileInfo) IsDir() bool {
	// Not applicable for S3 Objects, return default value
	return f.isDir
}

func (f *S3FileInfo) Sys() interface{} {
	// Not applicable for S3 Objects, return default value
	return f.fs
}

func (f *S3FileInfo) String() string {
	return fmt.Sprintf("S3FileInfo{Name: %s, Size: %d, ModTime: %v, IsDir: %t}", f.key, f.size, f.lastModified, f.isDir)
}
