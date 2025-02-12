package s3vfs

import (
	"oss.nandlabs.io/golly/l3"
	"oss.nandlabs.io/golly/vfs"
)

var (
	logger = l3.Get()
)

func init() {
	s3Fs := &S3Fs{}
	vfs.GetManager().Register(s3Fs)
}
