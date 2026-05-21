package source

import "os"

// Source represents a template content source.
type Source interface {
	IsModified() bool
	GetCacheKey() string
	GetContent() string
}

// FileSource loads templates from the filesystem.
type FileSource struct {
	filePath string
	modTime  int64
	content  string
	loaded   bool
}

// NewFileSource creates a FileSource for the given path.
func NewFileSource(filePath string) *FileSource {
	return &FileSource{filePath: filePath}
}

func (s *FileSource) IsModified() bool {
	info, err := os.Stat(s.filePath)
	if err != nil {
		return false
	}
	return info.ModTime().UnixNano() != s.modTime
}

func (s *FileSource) GetCacheKey() string {
	return s.filePath
}

func (s *FileSource) GetContent() string {
	if !s.loaded || s.IsModified() {
		data, err := os.ReadFile(s.filePath)
		if err != nil {
			return ""
		}
		s.content = string(data)
		info, _ := os.Stat(s.filePath)
		if info != nil {
			s.modTime = info.ModTime().UnixNano()
		}
		s.loaded = true
	}
	return s.content
}

// StringSource wraps a string as a template source.
type StringSource struct {
	content string
	key     string
}

// NewStringSource creates a StringSource.
func NewStringSource(content string) *StringSource {
	return &StringSource{content: content, key: content}
}

func (s *StringSource) IsModified() bool    { return false }
func (s *StringSource) GetCacheKey() string { return s.key }
func (s *StringSource) GetContent() string  { return s.content }
