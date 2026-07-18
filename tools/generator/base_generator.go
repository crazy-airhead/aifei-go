package generator

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed templates/_base.af
var baseTemplateContent string

// BaseGenerator generates base.go (always overwritten).
type BaseGenerator struct {
	GenerateShortSetter bool
}

// NewBaseGenerator creates a BaseGenerator with defaults.
func NewBaseGenerator() *BaseGenerator {
	return &BaseGenerator{
		GenerateShortSetter: true,
	}
}

// Generate generates base.go for a single table.
func (g *BaseGenerator) Generate(engine *Engine, info *TableInfo, outputDir string) error {
	data := g.buildData(info)
	content, err := engine.RenderTemplate(baseTemplateContent, data)
	if err != nil {
		return fmt.Errorf("render base template: %w", err)
	}

	pkgDir := filepath.Join(outputDir, info.PkgName)
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		return fmt.Errorf("create dir %s: %w", pkgDir, err)
	}

	target := filepath.Join(pkgDir, "base.go")
	if err := os.WriteFile(target, []byte(content), 0644); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	fmt.Printf("[aifei-gen] Generated %s\n", target)
	return nil
}

// buildData builds the template data map for base.go.
func (g *BaseGenerator) buildData(info *TableInfo) map[string]interface{} {
	util := &TemplateUtil{}

	// Collect imports
	importSet := make(map[string]bool)
	for _, f := range info.Fields {
		if pkg := util.ImportPath(f.GoType); pkg != "" {
			importSet[pkg] = true
		}
	}
	// Need "reflect" for Table.FieldTypes
	importSet["reflect"] = true

	var imports []string
	for imp := range importSet {
		imports = append(imports, imp)
	}

	// Build field names string
	fieldNames := make([]string, len(info.Fields))
	for i, f := range info.Fields {
		fieldNames[i] = f.Name
	}
	fieldNamesStr := strings.Join(fieldNames, ",")

	// Build primary keys quoted
	pkParts := make([]string, len(info.PrimaryKey))
	for i, pk := range info.PrimaryKey {
		pkParts[i] = `"` + pk + `"`
	}
	pkQuoted := strings.Join(pkParts, ", ")

	// Build generated columns quoted
	var generatedColumnsQuoted string
	var generatedColumnNames []string
	for _, f := range info.Fields {
		if f.IsGenerated {
			generatedColumnNames = append(generatedColumnNames, `"`+f.Name+`"`)
		}
	}
	if len(generatedColumnNames) > 0 {
		generatedColumnsQuoted = strings.Join(generatedColumnNames, ", ")
	} else {
		generatedColumnsQuoted = ""
	}

	// Non-JSON fields drive the typed getters/setters; JSON columns are
	// scaffolded in model.go, freeing the method name for a typed override.
	// FieldTypes still includes them (info.Fields is passed through below).
	var fields []*FieldInfo
	for _, f := range info.Fields {
		if f.IsJSON {
			continue
		}
		fields = append(fields, f)
	}

	// Short setter fields (nil when disabled)
	var shortSetterFields []*FieldInfo
	if g.GenerateShortSetter {
		shortSetterFields = fields
	}

	return map[string]interface{}{
		"pkgName":                info.PkgName,
		"imports":                imports,
		"tableName":              info.Name,
		"tableComment":           info.Remarks,
		"fieldNamesStr":          fieldNamesStr,
		"pkQuoted":               pkQuoted,
		"generatedColumnsQuoted": generatedColumnsQuoted,
		"fieldTypes":             info.Fields,
		"baseName":               info.BaseName,
		"fields":                 fields,
		"shortSetterFields":      shortSetterFields,
	}
}
