package generator

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed templates/_tables.af
var tablesTemplateContent string

// TablesGenerator generates tables.go (always overwritten).
type TablesGenerator struct{}

// NewTablesGenerator creates a TablesGenerator.
func NewTablesGenerator() *TablesGenerator {
	return &TablesGenerator{}
}

// Generate generates tables.go with all table metadata.
func (g *TablesGenerator) Generate(engine *Engine, infos []*TableInfo, outputDir, importRoot, outputPkgName, generatorPkg string) error {
	data := map[string]interface{}{
		"tableInfoList": infos,
		"importRoot":    importRoot,
		"outputPkgName": outputPkgName,
		"generatorPkg":  generatorPkg,
	}
	content := engine.RenderTemplate(tablesTemplateContent, data)

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create dir %s: %w", outputDir, err)
	}

	target := filepath.Join(outputDir, "tables.go")
	if err := os.WriteFile(target, []byte(content), 0644); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	fmt.Printf("[aifei-gen] Generated %s\n", target)
	return nil
}
