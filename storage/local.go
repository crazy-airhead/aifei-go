package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/crazy-airhead/aifei-go/log"
)

// LocalClient stores objects on the local filesystem, rooted at a directory,
// one subdirectory per bucket. It uses only the Go standard library.
type LocalClient struct {
	bucket string
	root   string
	mu     sync.Mutex // guards directory creation
	log    log.Logger
}

// NewLocalClient creates a filesystem-backed client for bucket rooted at root.
// The root directory is created if it does not exist.
func NewLocalClient(bucket, root string, logger log.Logger) (*LocalClient, error) {
	if bucket == "" {
		bucket = defaultBucketName
	}
	if root == "" {
		root = defaultBucketName
	}
	if err := os.MkdirAll(filepath.Join(root, bucket), 0o755); err != nil {
		return nil, fmt.Errorf("storage: create local root: %w", err)
	}
	if logger == nil {
		logger = log.Default()
	}
	return &LocalClient{bucket: bucket, root: root, log: logger}, nil
}

// Exists reports whether an object exists on disk.
func (c *LocalClient) Exists(key string) (bool, error) {
	info, err := os.Stat(c.resolve(key))
	if err == nil {
		return !info.IsDir(), nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("storage: stat %s: %w", key, err)
}

// TempURL is not supported by the local backend.
func (c *LocalClient) TempURL(key string, ttl time.Duration) (string, error) {
	return "", ErrUnsupported
}

// Get opens an object as a Media. Missing objects return (nil, nil).
func (c *LocalClient) Get(key string) (*Media, error) {
	path := c.resolve(key)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: stat %s: %w", key, err)
	}
	if info.IsDir() {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("storage: open %s: %w", key, err)
	}
	return &Media{body: file, contentType: mimeByExt(filepath.Base(path)), size: info.Size()}, nil
}

// Put writes media to disk, creating parent directories as needed.
func (c *LocalClient) Put(key string, media *Media) (*PutResult, error) {
	path, err := c.ensureFile(key)
	if err != nil {
		return nil, err
	}
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("storage: create %s: %w", key, err)
	}
	if _, err := io.Copy(out, media.Body()); err != nil {
		out.Close()
		return nil, fmt.Errorf("storage: write %s: %w", key, err)
	}
	if err := out.Close(); err != nil {
		return nil, fmt.Errorf("storage: close %s: %w", key, err)
	}
	parent, _ := filepath.Abs(filepath.Join(c.root, c.bucket))
	return &PutResult{Driver: string(StorageLocal), Bucket: c.bucket, Parent: parent, Key: key}, nil
}

// Delete removes an object. Removing a missing object is not an error.
func (c *LocalClient) Delete(key string) error {
	if err := os.Remove(c.resolve(key)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("storage: remove %s: %w", key, err)
	}
	return nil
}

// DeleteBatch removes multiple objects, collecting per-key failures.
func (c *LocalClient) DeleteBatch(keys []string) (*BatchResult, error) {
	res := &BatchResult{}
	for _, key := range keys {
		if err := c.Delete(key); err != nil {
			res.Partial = true
			res.Errors = append(res.Errors, KeyError{Key: key, Err: err})
		}
	}
	return res, nil
}

// resolve returns the on-disk path for key, honoring absolute-key compatibility
// (an absolute key is used verbatim, matching the legacy Java behavior).
func (c *LocalClient) resolve(key string) string {
	if filepath.IsAbs(key) {
		return key
	}
	return filepath.Join(c.root, c.bucket, key)
}

// ensureFile creates the parent directories for key and returns its path.
func (c *LocalClient) ensureFile(key string) (string, error) {
	path := key
	if !filepath.IsAbs(key) {
		path = filepath.Join(c.root, c.bucket, key)
	}
	dir := filepath.Dir(path)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("storage: mkdir %s: %w", dir, err)
	}
	return path, nil
}
