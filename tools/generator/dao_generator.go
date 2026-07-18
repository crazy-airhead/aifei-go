package generator

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed templates/_dao.af
var daoTemplateContent string

// DaoGenerator generates dao.go (skipped if exists).
type DaoGenerator struct{}

// NewDaoGenerator creates a DaoGenerator.
func NewDaoGenerator() *DaoGenerator {
	return &DaoGenerator{}
}

// Generate generates dao.go. Skips if the file already exists.
func (g *DaoGenerator) Generate(engine *Engine, info *TableInfo, outputDir string) error {
	data := g.buildData(info)
	content, err := engine.RenderTemplate(daoTemplateContent, data)
	if err != nil {
		return fmt.Errorf("render dao template: %w", err)
	}

	pkgDir := filepath.Join(outputDir, info.PkgName)
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		return fmt.Errorf("create dir %s: %w", pkgDir, err)
	}

	target := filepath.Join(pkgDir, "dao.go")

	// Skip if file exists — user may have added custom logic
	if _, err := os.Stat(target); err == nil {
		fmt.Printf("[aifei-gen] Skip %s (already exists)\n", target)
		return nil
	}

	if err := os.WriteFile(target, []byte(content), 0644); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	fmt.Printf("[aifei-gen] Generated %s\n", target)
	return nil
}

// buildData builds the template data map for dao.go.
func (g *DaoGenerator) buildData(info *TableInfo) map[string]interface{} {
	hasSinglePK := len(info.PrimaryKey) == 1
	pkGoType := "interface{}"
	if hasSinglePK {
		for _, f := range info.Fields {
			if f.Name == info.PrimaryKey[0] {
				pkGoType = f.GoType
				break
			}
		}
	}

	return map[string]interface{}{
		"pkgName":     info.PkgName,
		"tableName":   info.Name,
		"structName":  info.StructName,
		"baseName":    info.BaseName,
		"hasSinglePK": hasSinglePK,
		"pkGoType":    pkGoType,
	}
}
