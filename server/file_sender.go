package server

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// FileSender configures a file download or generated-content export. Business
// code populates it inside an Out.OfFile(func(*FileSender){...}) closure; the
// IoHandler performs all I/O (it never hands the raw http.ResponseWriter to
// business code). Mirrors Java aifei-vip-arch cn.aifei.vip.arch.http.FileSender.
//
// The body to send is chosen in this order:
//  1. Data (in-memory bytes, e.g. an excel export)
//  2. Reader (a stream of generated content)
//  3. FileName (a path on disk, resolved under the IoHandler's download base)
type FileSender struct {
	fileName    string    // disk path, resolved under downloadBase, for file download
	saveAsName  string    // client-side filename; defaults to FileName's base
	contentType string    // optional; guessed from the save-as extension if empty
	data        []byte    // generated content (alternative to Reader/FileName)
	reader      io.Reader // generated content as a stream (alternative to Data/FileName)
	size        int64     // explicit body size (Content-Length); 0 = derive/unknown
}

// SetFileName points the download at a path on disk (relative to the IoHandler's
// download base). Returns s for chaining.
func (s *FileSender) SetFileName(fileName string) *FileSender {
	s.fileName = strings.TrimSpace(fileName)
	return s
}

// SetSaveAsName sets the client-side filename. It must not contain a path.
// Returns s for chaining.
func (s *FileSender) SetSaveAsName(saveAsName string) *FileSender {
	saveAsName = strings.TrimSpace(saveAsName)
	if strings.ContainsAny(saveAsName, "/\\") {
		panic("FileSender.SetSaveAsName: name must not contain a path separator")
	}
	s.saveAsName = saveAsName
	return s
}

// SetContentType overrides the Content-Type. Returns s for chaining.
func (s *FileSender) SetContentType(contentType string) *FileSender {
	s.contentType = strings.TrimSpace(contentType)
	return s
}

// SetData sets the in-memory body (e.g. generated excel bytes). Returns s for
// chaining.
func (s *FileSender) SetData(data []byte) *FileSender {
	s.data = data
	return s
}

// SetReader sets the body as a stream of generated content. Returns s for
// chaining.
func (s *FileSender) SetReader(r io.Reader) *FileSender {
	s.reader = r
	return s
}

// SetSize sets an explicit Content-Length (useful when the body is a Reader of
// known length, e.g. a storage object). 0 leaves it derived (Data/disk) or
// unknown (Reader). Returns s for chaining.
func (s *FileSender) SetSize(size int64) *FileSender {
	s.size = size
	return s
}

// send writes the download to w: Content-Type, Content-Disposition (attachment),
// optional Content-Length, and the streamed body. downloadBase is the root for
// disk downloads (FileName); it may be empty to resolve FileName against the
// working directory.
func (s *FileSender) send(w http.ResponseWriter, downloadBase string) error {
	saveAs := s.saveAsName
	if saveAs == "" {
		saveAs = filepath.Base(s.fileName)
	}

	body, length, err := s.body(downloadBase)
	if err != nil {
		return err
	}
	if s.size > 0 {
		length = s.size
	}
	if closer, ok := body.(io.Closer); ok {
		defer closer.Close()
	}

	contentType := s.contentType
	if contentType == "" {
		contentType = contentTypeForExt(filepath.Ext(saveAs))
	}

	hdr := w.Header()
	hdr.Set("Content-Type", contentType)
	if saveAs != "" {
		hdr.Set("Content-Disposition", `attachment; filename*=UTF-8''`+url.PathEscape(saveAs))
	}
	if length >= 0 {
		hdr.Set("Content-Length", fmt.Sprintf("%d", length))
	}

	_, err = io.Copy(w, body)
	return err
}

// body resolves the response body and its length (-1 when unknown) per the
// Data > Reader > FileName order. Disk files are opened relative to downloadBase.
func (s *FileSender) body(downloadBase string) (io.Reader, int64, error) {
	switch {
	case s.data != nil:
		return bytes.NewReader(s.data), int64(len(s.data)), nil
	case s.reader != nil:
		return s.reader, -1, nil
	case s.fileName != "":
		path := s.fileName
		if downloadBase != "" && !filepath.IsAbs(path) {
			path = filepath.Join(downloadBase, path)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, 0, fmt.Errorf("file not found: %s", s.fileName)
		}
		if info.IsDir() {
			return nil, 0, fmt.Errorf("not a file: %s", s.fileName)
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, 0, err
		}
		return f, info.Size(), nil
	default:
		return nil, 0, fmt.Errorf("FileSender has no content: set Data, Reader, or FileName")
	}
}

// contentTypeForExt returns the MIME type for ext (including the leading dot),
// falling back to the common-download table and then application/octet-stream.
func contentTypeForExt(ext string) string {
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	if ct, ok := extToMIME[strings.ToLower(strings.TrimPrefix(ext, "."))]; ok {
		return ct
	}
	return "application/octet-stream"
}

// extToMIME holds common download MIME types (IANA/MDN), used as a fallback when
// the standard mime package has no mapping. Mirrors Java FileSender.EXT_TO_MIME.
var extToMIME = map[string]string{
	// Archives
	"zip": "application/zip",
	"rar": "application/vnd.rar",
	"gz":  "application/gzip",
	"tar": "application/x-tar",
	"7z":  "application/x-7z-compressed",

	// Office (legacy)
	"xls": "application/vnd.ms-excel",
	"doc": "application/msword",
	"ppt": "application/vnd.ms-powerpoint",

	// Office (OpenXML)
	"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",

	// Documents / text
	"pdf": "application/pdf",
	"csv": "text/csv",
	"txt": "text/plain",
	"md":  "text/markdown",

	// Images
	"png":  "image/png",
	"jpg":  "image/jpeg",
	"jpeg": "image/jpeg",
	"gif":  "image/gif",

	// Audio / video
	"mp3":  "audio/mpeg",
	"mp4":  "video/mp4",
	"mpeg": "video/mpeg",
	"avi":  "video/x-msvideo",
}
