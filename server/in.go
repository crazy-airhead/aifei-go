package server

import (
	"errors"
	"net/http"

	"github.com/crazy-airhead/aifei-go/aifei"
	aifeihttp "github.com/crazy-airhead/aifei-go/http"
)

// In implements aifei.Input by embedding aifeihttp.HttpContext.
//
// It reuses HttpContext's proven request-reading implementation instead of
// duplicating it, keeping server a thin convenience layer over http. The
// core aifei.Input contract (Param: GetStr/GetBean/...; Meta: Path/Header/
// Context/Body), the HTTP-specific HttpContext methods (Method/RemoteIP/
// Cookie, via aifeihttp.HTTPMeta), and SetParams are all promoted from the
// embedded HttpContext. The HTTP adapter (NewIoHandler) builds *In for every
// request, so *In methods like GetFile/GetFiles are reachable from services.
type In struct {
	*aifeihttp.HttpContext
}

// Compile-time guarantee that *In satisfies aifei.Input.
var _ aifei.Input = (*In)(nil)

// NewIn creates an In from an http.Request.
func NewIn(r *http.Request) *In {
	return &In{HttpContext: aifeihttp.NewInput(r)}
}

// ---- Upload retrieval (mirror Java aifei In.getUploadedFiles) ----

// ErrNoUpload is returned when a request carries no multipart upload for the
// requested field.
var ErrNoUpload = errors.New("no file uploaded")

// defaultMultipartMemory is the in-memory threshold (32 MiB) before a parsed
// upload spills to a temp file, matching net/http's r.FormFile default.
const defaultMultipartMemory int64 = 32 << 20

// GetFile returns the upload under the given form field name. Pass "" to get
// the first upload regardless of field name (mirrors Java @Para(name = "")).
//
// After GetFile returns, multipart text fields are parsed too and reachable
// via in.GetStr/GetInt (GetFile triggers r.FormFile → ParseMultipartForm,
// which fills r.Form with the text parts).
func (in *In) GetFile(name string) (*UploadedFile, error) {
	r := in.Request

	if name != "" {
		_, header, err := r.FormFile(name)
		if err != nil {
			if errors.Is(err, http.ErrMissingFile) {
				return nil, ErrNoUpload
			}
			return nil, err
		}
		return &UploadedFile{fieldName: name, header: header}, nil
	}

	// name == "": ensure the multipart body is parsed, then take the first upload.
	if err := r.ParseMultipartForm(defaultMultipartMemory); err != nil {
		return nil, err
	}
	if r.MultipartForm == nil {
		return nil, ErrNoUpload
	}
	for field, hs := range r.MultipartForm.File {
		if len(hs) > 0 {
			return &UploadedFile{fieldName: field, header: hs[0]}, nil
		}
	}
	return nil, ErrNoUpload
}

// GetFiles returns all uploads under the given field name (mirrors Java
// In.getUploadedFiles()). Pass "" to return every upload in the request.
func (in *In) GetFiles(name string) ([]*UploadedFile, error) {
	r := in.Request
	if err := r.ParseMultipartForm(defaultMultipartMemory); err != nil {
		return nil, err
	}
	if r.MultipartForm == nil {
		return nil, ErrNoUpload
	}
	var out []*UploadedFile
	if name != "" {
		for _, h := range r.MultipartForm.File[name] {
			out = append(out, &UploadedFile{fieldName: name, header: h})
		}
		return out, nil
	}
	for field, hs := range r.MultipartForm.File {
		for _, h := range hs {
			out = append(out, &UploadedFile{fieldName: field, header: h})
		}
	}
	return out, nil
}
