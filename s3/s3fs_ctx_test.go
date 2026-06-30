package s3

import (
	"testing"

	"oss.nandlabs.io/golly/vfs"
)

// TestS3FS_ImplementsVFileSystemCtx is the load-bearing test for this
// downstream impl: as long as S3FS satisfies vfs.VFileSystemCtx, the
// package-level dispatchers in golly (vfs.OpenCtx, vfs.CopyCtx, …) will
// route through our ctx-aware methods rather than the goroutine
// fallback. Real ctx propagation correctness past that boundary is the
// AWS SDK's responsibility — it takes context on every public call.
//
// An end-to-end cancellation test against a fake S3 endpoint is left to
// integration tests in the consuming project.
func TestS3FS_ImplementsVFileSystemCtx(t *testing.T) {
	var _ vfs.VFileSystemCtx = (*S3FS)(nil)
}

// TestS3FS_ImplementsLister guarantees vfs.ListIter dispatches through
// S3FS.ListIter rather than falling back to the eager List slice (which
// would materialize million-key prefixes into one allocation).
func TestS3FS_ImplementsLister(t *testing.T) {
	var _ vfs.Lister = (*S3FS)(nil)
}

// TestS3File_ImplementsRangeReader guarantees vfs.ReadRange dispatches
// to a native S3 ranged GET instead of Seek+Read (which on cloud
// backends typically downloads the whole object).
func TestS3File_ImplementsRangeReader(t *testing.T) {
	var _ vfs.RangeReader = (*S3File)(nil)
}
