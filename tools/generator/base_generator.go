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

// FieldEntry holds pre-computed field data for the base template.
type FieldEntry struct {
	Name      string
	AttrName  string
	GoType    string
	RowGetter string
	ShortName string
}

// FieldTypeEntry holds a field-type pair for the Table.FieldTypes map.
type FieldTypeEntry struct {
	Name string
	Zero string
}

// Generate generates base.go for a single table.
func (g *BaseGenerator) Generate(engine *Engine, info *TableInfo, outputDir string) error {
	data := g.buildData(info)
	content := engine.RenderTemplate(baseTemplateContent, data)

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

	// Build field type entries
	var fieldTypes []FieldTypeEntry
	for _, f := range info.Fields {
		fieldTypes = append(fieldTypes, FieldTypeEntry{
			Name: f.Name,
			Zero: util.ZeroValue(f.GoType),
		})
	}

	// Build field data. JSON columns are skipped here: their getters/setters are
	// generated into model.go instead, freeing the method name for a typed
	// (struct) override. FieldTypes and the INSERT column list still include them.
	var fields []FieldEntry
	for _, f := range info.Fields {
		if f.IsJSON {
			continue
		}
		shortName := f.AttrName
		if IsGoKeyword(shortName) {
			shortName += "_"
		}
		fields = append(fields, FieldEntry{
			Name:      f.Name,
			AttrName:  f.AttrName,
			GoType:    f.GoType,
			RowGetter: util.RowGetter(f.GoType),
			ShortName: shortName,
		})
	}

	// Build short setter fields (empty slice when disabled)
	var shortSetterFields []FieldEntry
	if g.GenerateShortSetter {
		shortSetterFields = fields
	}

	return map[string]interface{}{
		"pkgName":           info.PkgName,
		"imports":           imports,
		"tableName":         info.Name,
		"fieldNamesStr":     fieldNamesStr,
		"pkQuoted":          pkQuoted,
		"fieldTypes":        fieldTypes,
		"baseName":          info.BaseName,
		"fields":            fields,
		"shortSetterFields": shortSetterFields,
	}
}
