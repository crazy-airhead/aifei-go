package generator

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

//go:embed templates/_init.af
var initTemplateContent string

// InitGenerator generates init.go (always overwritten).
type InitGenerator struct{}

// NewInitGenerator creates an InitGenerator.
func NewInitGenerator() *InitGenerator {
	return &InitGenerator{}
}

// Generate generates init.go with blank imports for self-registration.
// Multiple tables may share one package (Qualified mode); each package is
// imported once.
func (g *InitGenerator) Generate(engine *Engine, infos []*TableInfo, outputDir, importRoot, outputPkgName string) error {
	outputPkgName = sanitizePackageName(outputPkgName)

	seen := make(map[string]bool)
	var pkgs []string
	for _, info := range infos {
		if !seen[info.PkgName] {
			seen[info.PkgName] = true
			pkgs = append(pkgs, info.PkgName)
		}
	}

	data := map[string]interface{}{
		"pkgs":          pkgs,
		"importRoot":    importRoot,
		"outputPkgName": outputPkgName,
	}
	content, err := engine.RenderTemplate(initTemplateContent, data)
	if err != nil {
		return fmt.Errorf("render init template: %w", err)
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create dir %s: %w", outputDir, err)
	}

	target := filepath.Join(outputDir, "init.go")
	if err := os.WriteFile(target, []byte(content), 0644); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	fmt.Printf("[aifei-gen] Generated %s\n", target)
	return nil
}

// sanitizePackageName turns an arbitrary output-dir basename into a valid Go
// package identifier — the basename is caller/tooling controlled and may
// contain '-', '.', or start with a digit.
func sanitizePackageName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || r == '_':
			b.WriteRune(r)
		case unicode.IsDigit(r) && b.Len() > 0:
			b.WriteRune(r)
		case unicode.IsDigit(r):
			b.WriteString("x") // leading digit
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "generated"
	}
	return b.String()
}
