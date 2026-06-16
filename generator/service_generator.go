package generator

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed templates/_service.af
var serviceTemplateContent string

// ServiceGenerator generates service.go (not overwritten on re-generation).
type ServiceGenerator struct {
	// APIPrefix is the URL prefix for service routes, e.g. "/api/v1".
	APIPrefix string
}

// NewServiceGenerator creates a ServiceGenerator.
func NewServiceGenerator() *ServiceGenerator {
	return &ServiceGenerator{}
}

// Generate generates service.go for a single table.
func (g *ServiceGenerator) Generate(engine *Engine, info *TableInfo, outputDir string) error {
	pkgDir := filepath.Join(outputDir, info.PkgName)
	target := filepath.Join(pkgDir, "service.go")

	if _, err := os.Stat(target); err == nil {
		fmt.Printf("[aifei-gen] Skipped %s (already exists)\n", target)
		return nil
	}

	data := g.buildData(info)
	content := engine.RenderTemplate(serviceTemplateContent, data)

	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		return fmt.Errorf("create dir %s: %w", pkgDir, err)
	}

	if err := os.WriteFile(target, []byte(content), 0644); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	fmt.Printf("[aifei-gen] Generated %s\n", target)
	return nil
}

// buildData builds the template data map for service.go.
func (g *ServiceGenerator) buildData(info *TableInfo) map[string]interface{} {
	hasSinglePK := len(info.PrimaryKey) == 1

	var pkGoType, pkName string
	if hasSinglePK {
		pkName = info.PrimaryKey[0]
		for _, f := range info.Fields {
			if f.Name == pkName {
				pkGoType = f.GoType
				break
			}
		}
	}

	servicePath := ToCamelCase(info.StructName)

	return map[string]interface{}{
		"apiPrefix":       g.APIPrefix,
		"pkgName":         info.PkgName,
		"structName":      info.StructName,
		"servicePath":     servicePath,
		"tableName":       info.Name,
		"pkName":          pkName,
		"hasSinglePK":     hasSinglePK,
		"pkParamParse":    buildPKParamParse(pkGoType),
		"needStrconv":     pkGoType == "int" || pkGoType == "int64" || pkGoType == "int32" || pkGoType == "int16" || pkGoType == "int8",
		"queryConditions": buildQueryConditions(info, info.PrimaryKey),
	}
}

func buildQueryConditions(info *TableInfo, primaryKeys []string) string {
	pkSet := make(map[string]bool)
	for _, pk := range primaryKeys {
		pkSet[pk] = true
	}

	var parts []string
	first := true
	for _, f := range info.Fields {
		if pkSet[f.Name] {
			continue
		}
		if first {
			parts = append(parts, fmt.Sprintf("#where(%s, '=', %s)", f.Name, f.Name))
			first = false
		} else {
			parts = append(parts, fmt.Sprintf("#and(%s, '=', %s)", f.Name, f.Name))
		}
	}
	return strings.Join(parts, "\n")
}

// buildPKParamParse generates Go code to parse a PK value from in.Param("id").
func buildPKParamParse(goType string) string {
	switch goType {
	case "int":
		return `id, err := strconv.Atoi(in.Param("id"))
		if err != nil {
			return server.Fail("invalid id")
		}`
	case "int64":
		return `id, err := strconv.ParseInt(in.Param("id"), 10, 64)
		if err != nil {
			return server.Fail("invalid id")
		}`
	case "string":
		return `id := in.Param("id")`
	default:
		return `id := in.Param("id")`
	}
}
