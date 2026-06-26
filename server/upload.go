package server

import (
	"io"
	"mime"
	"mime/multipart"
	"path/filepath"
)

// UploadedFile wraps a single multipart upload. It mirrors the Java aifei
// UploadedFile: service code reads content and metadata without touching
// *http.Request or multipart parsing, and without being forced to save to disk
// (content is read on demand via Open/Bytes, like Java's getInputStream).
type UploadedFile struct {
	fieldName string
	header    *multipart.FileHeader
}

// FieldName returns the form field name that carried this upload (e.g. "file").
func (f *UploadedFile) FieldName() string { return f.fieldName }

// FileName returns the original client-side file name.
func (f *UploadedFile) FileName() string { return f.header.Filename }

// Size returns the upload size in bytes.
func (f *UploadedFile) Size() int64 { return f.header.Size }

// Extension returns the filename extension including the leading dot
// (e.g. ".png"); empty when there is none.
func (f *UploadedFile) Extension() string { return filepath.Ext(f.header.Filename) }

// ContentType returns the upload's content type, falling back to a guess from
// the filename extension, then to application/octet-stream.
func (f *UploadedFile) ContentType() string {
	if ct := f.header.Header.Get("Content-Type"); ct != "" {
		return ct
	}
	if ct := mime.TypeByExtension(f.Extension()); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// Open returns a reader over the upload content. The caller must close it.
// Mirrors Java UploadedFile.getInputStream().
func (f *UploadedFile) Open() (multipart.File, error) {
	return f.header.Open()
}

// Bytes reads the entire upload into memory. Convenient for small/medium files
// that need full-content processing (e.g. md5). Prefer Open for streaming or
// large uploads.
func (f *UploadedFile) Bytes() ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
