// Package s3 implements the golly VFS (Virtual File System) interface for AWS S3.
//
// It registers itself with the golly VFS manager on import, supporting the "s3" URL scheme.
// URLs follow the format: s3://bucket-name/key/path
//
// Configuration is resolved via the awscfg package. Register an awscfg.Config
// before using the s3 package:
//
//	cfg := awscfg.NewConfig("us-east-1")
//	awscfg.Manager.Register("s3", cfg)
//
//	// Then use via the VFS manager:
//	file, err := vfs.GetManager().OpenRaw("s3://my-bucket/path/to/file.txt")
package s3
