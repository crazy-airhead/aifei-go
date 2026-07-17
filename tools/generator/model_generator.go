package generator

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed templates/_model.af
var modelTemplateContent string

// ModelGenerator generates the model struct file (skipped if exists).
type ModelGenerator struct{}

// NewModelGenerator creates a ModelGenerator.
func NewModelGenerator() *ModelGenerator {
	return &ModelGenerator{}
}

// Generate generates the model struct file. Skips if the file already exists.
func (g *ModelGenerator) Generate(engine *Engine, info *TableInfo, outputDir string) error {
	data := g.buildData(info)
	content := engine.RenderTemplate(modelTemplateContent, data)

	pkgDir := filepath.Join(outputDir, info.PkgName)
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		return fmt.Errorf("create dir %s: %w", pkgDir, err)
	}

	fileName := "model.go"
	target := filepath.Join(pkgDir, fileName)

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

// buildData builds the template data map for model.go.
//
// jsonFields carries the table's JSON/JSONB columns so the model template can
// emit default string getters/setters as scaffolding. A user upgrades a column
// to a struct type by defining the type, registering it in Table.FieldTypes,
// and editing the scaffolded methods (see the template comments).
func (g *ModelGenerator) buildData(info *TableInfo) map[string]interface{} {
	var jsonFields []*FieldInfo
	for _, f := range info.Fields {
		if f.IsJSON {
			jsonFields = append(jsonFields, f)
		}
	}
	return map[string]interface{}{
		"pkgName":      info.PkgName,
		"tableName":    info.Name,
		"tableComment": info.Remarks,
		"structName":   info.StructName,
		"baseName":     info.BaseName,
		"jsonFields":   jsonFields,
	}
}
