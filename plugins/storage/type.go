package storage

import "strings"

// defaultBucketName is the bucket name used when none is configured, matching
// the Java StorageConstant.LOCAL_DEFAULT.
const defaultBucketName = "storage"

// StorageType identifies a storage backend.
type StorageType string

const (
	// StorageLocal is the filesystem backend.
	StorageLocal StorageType = "local"
	// StorageS3 is the S3-compatible backend (AWS S3, Minio, Aliyun OSS,
	// Tencent COS, ...).
	StorageS3 StorageType = "s3"
)

// storageTypeOf infers the backend from the configured driver and endpoint.
// An explicit driver wins; otherwise an http(s) endpoint implies S3; otherwise
// the backend defaults to local.
func storageTypeOf(driver, endpoint string) StorageType {
	switch strings.ToLower(driver) {
	case "s3":
		return StorageS3
	case "local":
		return StorageLocal
	}
	// driver empty or unknown: infer from the endpoint scheme.
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return StorageS3
	}
	return StorageLocal
}
