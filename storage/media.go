package storage

import (
	"bytes"
	"io"
	"mime"
	"path/filepath"
)

// defaultContentType is the fallback content type when none can be inferred.
const defaultContentType = "text/plain; charset=utf-8"

// Media wraps a file's content together with its content type and size. It is
// the unit of data exchanged through the storage Client — the Go counterpart of
// the Java Media class.
type Media struct {
	body        io.Reader
	contentType string
	size        int64 // 0 means unknown
}

// NewMedia creates a Media from a reader and a content type. The size is
// treated as unknown.
func NewMedia(body io.Reader, contentType string) *Media {
	return &Media{body: body, contentType: contentType}
}

// NewMediaWithSize is like NewMedia but records a known content size, which some
// backends (e.g. S3) use to upload efficiently.
func NewMediaWithSize(body io.Reader, contentType string, size int64) *Media {
	return &Media{body: body, contentType: contentType, size: size}
}

// OfBytes creates a Media from a byte slice.
func OfBytes(b []byte, contentType string) *Media {
	return &Media{body: bytes.NewReader(b), contentType: contentType, size: int64(len(b))}
}

// OfString creates a Media from a string.
func OfString(s, contentType string) *Media {
	return OfBytes([]byte(s), contentType)
}

// OfFileName creates a Media from a reader, inferring the content type from the
// file name's extension.
func OfFileName(fileName string, body io.Reader) *Media {
	return &Media{body: body, contentType: mimeByExt(fileName)}
}

// OfFileNameWithSize is like OfFileName but records a known content size.
func OfFileNameWithSize(fileName string, body io.Reader, size int64) *Media {
	return &Media{body: body, contentType: mimeByExt(fileName), size: size}
}

// ContentType returns the media content type, if known.
func (m *Media) ContentType() string { return m.contentType }

// Size returns the content size in bytes, or 0 when unknown.
func (m *Media) Size() int64 { return m.size }

// Body returns the underlying reader. The caller is responsible for closing it
// when applicable (see Close).
func (m *Media) Body() io.Reader { return m.body }

// Bytes reads the whole body into a byte slice. It does not close the body.
func (m *Media) Bytes() ([]byte, error) {
	return io.ReadAll(m.body)
}

// String reads the whole body as a string. It does not close the body.
func (m *Media) String() (string, error) {
	b, err := io.ReadAll(m.body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Close closes the body if it implements io.Closer. It is a no-op otherwise.
func (m *Media) Close() error {
	if c, ok := m.body.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// mimeByExt guesses the content type from a file name's extension, falling back
// to a sensible default for unknown extensions.
func mimeByExt(name string) string {
	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		return ct
	}
	return defaultContentType
}
