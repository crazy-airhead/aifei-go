package storage

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/crazy-airhead/aifei-go/log"
)

// S3Client stores objects on an S3-compatible service (AWS S3, Minio, Aliyun
// OSS, Tencent COS, ...) via minio-go.
type S3Client struct {
	bucket   string
	region   string
	client   *minio.Client
	autoMake bool
	log      log.Logger

	once    sync.Once
	initErr error
}

// S3Options configure the S3-compatible client.
type S3Options struct {
	Endpoint         string
	Region           string
	AccessKey        string
	SecretKey        string
	AutoCreateBucket bool
}

// NewS3Client creates an S3-compatible client for bucket. The endpoint may be
// given as "host:port" or "scheme://host:port".
func NewS3Client(bucket string, opts S3Options, logger log.Logger) (*S3Client, error) {
	if bucket == "" {
		bucket = defaultBucketName
	}
	host, secure, err := parseEndpoint(opts.Endpoint)
	if err != nil {
		return nil, err
	}
	mc, err := minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(opts.AccessKey, opts.SecretKey, ""),
		Secure: secure,
		Region: opts.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: init s3 client: %w", err)
	}
	if logger == nil {
		logger = log.Default()
	}
	return &S3Client{bucket: bucket, region: opts.Region, client: mc, autoMake: opts.AutoCreateBucket, log: logger}, nil
}

// Exists reports whether an object exists.
func (c *S3Client) Exists(key string) (bool, error) {
	if err := c.ensureBucket(context.Background()); err != nil {
		return false, err
	}
	if _, err := c.client.StatObject(context.Background(), c.bucket, key, minio.StatObjectOptions{}); err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("storage: stat %s: %w", key, err)
	}
	return true, nil
}

// TempURL returns a presigned GET URL valid for ttl.
func (c *S3Client) TempURL(key string, ttl time.Duration) (string, error) {
	if err := c.ensureBucket(context.Background()); err != nil {
		return "", err
	}
	u, err := c.client.PresignedGetObject(context.Background(), c.bucket, key, ttl, nil)
	if err != nil {
		return "", fmt.Errorf("storage: presign %s: %w", key, err)
	}
	return u.String(), nil
}

// Get fetches an object as a Media. Missing objects return (nil, nil). The
// caller must close the returned Media.
func (c *S3Client) Get(key string) (*Media, error) {
	if err := c.ensureBucket(context.Background()); err != nil {
		return nil, err
	}
	info, err := c.client.StatObject(context.Background(), c.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: stat %s: %w", key, err)
	}
	obj, err := c.client.GetObject(context.Background(), c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("storage: get %s: %w", key, err)
	}
	return &Media{body: obj, contentType: info.ContentType, size: info.Size}, nil
}

// Put uploads media to the bucket.
func (c *S3Client) Put(key string, media *Media) (*PutResult, error) {
	if err := c.ensureBucket(context.Background()); err != nil {
		return nil, err
	}
	ct := media.ContentType()
	if ct == "" {
		ct = mimeByExt(key)
	}
	if ct == "" {
		ct = defaultContentType
	}
	size := media.Size()
	if size <= 0 {
		size = -1 // minio: stream with multipart when size unknown
	}
	if _, err := c.client.PutObject(context.Background(), c.bucket, key, media.Body(), size, minio.PutObjectOptions{ContentType: ct}); err != nil {
		return nil, fmt.Errorf("storage: put %s: %w", key, err)
	}
	return &PutResult{Driver: string(StorageS3), Bucket: c.bucket, Key: key}, nil
}

// Delete removes an object.
func (c *S3Client) Delete(key string) error {
	if err := c.ensureBucket(context.Background()); err != nil {
		return err
	}
	if err := c.client.RemoveObject(context.Background(), c.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("storage: remove %s: %w", key, err)
	}
	return nil
}

// DeleteBatch removes multiple objects, collecting per-key failures.
func (c *S3Client) DeleteBatch(keys []string) (*BatchResult, error) {
	if err := c.ensureBucket(context.Background()); err != nil {
		return nil, err
	}
	objCh := make(chan minio.ObjectInfo, len(keys))
	for _, k := range keys {
		objCh <- minio.ObjectInfo{Key: k}
	}
	close(objCh)

	res := &BatchResult{}
	for e := range c.client.RemoveObjects(context.Background(), c.bucket, objCh, minio.RemoveObjectsOptions{}) {
		res.Partial = true
		res.Errors = append(res.Errors, KeyError{Key: e.ObjectName, Err: e.Err})
	}
	return res, nil
}

// ensureBucket creates the bucket once on first use when AutoCreateBucket is set.
func (c *S3Client) ensureBucket(ctx context.Context) error {
	c.once.Do(func() {
		if !c.autoMake {
			return
		}
		exists, err := c.client.BucketExists(ctx, c.bucket)
		if err != nil {
			c.initErr = fmt.Errorf("storage: check bucket %s: %w", c.bucket, err)
			return
		}
		if !exists {
			if err := c.client.MakeBucket(ctx, c.bucket, minio.MakeBucketOptions{Region: c.region}); err != nil {
				c.initErr = fmt.Errorf("storage: create bucket %s: %w", c.bucket, err)
			}
		}
	})
	return c.initErr
}

// isNotFound reports whether err is a "no such object" response.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	resp := minio.ToErrorResponse(err)
	code := string(resp.Code)
	return code == "NoSuchKey" || code == "NoSuchObject"
}

// parseEndpoint splits an endpoint into host[:port] and a secure flag. It
// accepts both "host:port" and "scheme://host:port" forms.
func parseEndpoint(endpoint string) (host string, secure bool, err error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", false, errors.New("storage: empty s3 endpoint")
	}
	if strings.Contains(endpoint, "://") {
		u, perr := url.Parse(endpoint)
		if perr != nil {
			return "", false, fmt.Errorf("storage: parse s3 endpoint: %w", perr)
		}
		return u.Host, u.Scheme == "https", nil
	}
	return endpoint, false, nil
}
