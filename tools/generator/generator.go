package generator

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/crazy-airhead/aifei-go/enjoy"
)

// Engine wraps an Enjoy template engine for code generation.
type Engine struct {
	enjoy         *enjoy.Engine
	templateCache sync.Map // string → *enjoy.Template
}

// NewEngine creates an Engine with shared utility methods registered.
func NewEngine() *Engine {
	e := enjoy.NewEngine("generator")
	e.AddSharedObject("u", &TemplateUtil{})

	return &Engine{enjoy: e}
}

// RenderTemplate compiles (or fetches cached) a template and renders it.
func (e *Engine) RenderTemplate(content string, data map[string]interface{}) (string, error) {
	var tpl *enjoy.Template
	if cached, ok := e.templateCache.Load(content); ok {
		tpl = cached.(*enjoy.Template)
	} else {
		tpl = e.enjoy.GetTemplateByString(content)
		e.templateCache.Store(content, tpl)
	}
	return tpl.RenderToString0(data)
}

// Generator is the code generator entry point.
type Generator struct {
	pool      *sql.DB
	dialect   MetaDialect
	outputDir string

	metaReader       *MetaReader
	baseGenerator    *BaseGenerator
	modelGenerator   *ModelGenerator
	daoGenerator     *DaoGenerator
	tablesGenerator  *TablesGenerator
	serviceGenerator *ServiceGenerator
	engine           *Engine

	// importRoot is the Go import path for the generated output directory.
	importRoot string

	// TablePrefix is stripped from table names before naming, e.g. "t_" strips "t_user" → "user".
	TablePrefix string

	// Naming functions (customizable)
	PkgNameFunc    func(string) string // table name → package name
	StructNameFunc func(string) string // table name → struct name
	BaseNameFunc   func(string) string // struct name → base struct name
}

// New creates a Generator.
func New(pool *sql.DB, dialect MetaDialect, outputDir, importRoot string) *Generator {
	util := &TemplateUtil{}
	return &Generator{
		pool:             pool,
		dialect:          dialect,
		outputDir:        outputDir,
		importRoot:       importRoot,
		metaReader:       NewMetaReader(),
		baseGenerator:    NewBaseGenerator(),
		modelGenerator:   NewModelGenerator(),
		daoGenerator:     NewDaoGenerator(),
		tablesGenerator:  NewTablesGenerator(),
		serviceGenerator: NewServiceGenerator(),
		engine:           NewEngine(),
		PkgNameFunc:      util.PkgName,
		StructNameFunc:   util.StructName,
		BaseNameFunc:     util.BaseName,
	}
}

// ConfigMetaReader configures the MetaReader.
func (g *Generator) ConfigMetaReader(fn func(*MetaReader)) *Generator {
	fn(g.metaReader)
	return g
}

// ConfigBaseGenerator configures the BaseGenerator.
func (g *Generator) ConfigBaseGenerator(fn func(*BaseGenerator)) *Generator {
	fn(g.baseGenerator)
	return g
}

// ConfigServiceGenerator configures the ServiceGenerator.
func (g *Generator) ConfigServiceGenerator(fn func(*ServiceGenerator)) *Generator {
	fn(g.serviceGenerator)
	return g
}

// Generate reads database metadata and generates model code.
func (g *Generator) Generate() error {
	fmt.Println("[aifei-gen] Starting code generation...")
	fmt.Printf("[aifei-gen] Output directory: %s\n", g.outputDir)

	// 1. Read table metadata
	tableInfos, err := g.metaReader.Read(g.pool, g.dialect)
	if err != nil {
		return fmt.Errorf("read metadata: %w", err)
	}

	if len(tableInfos) == 0 {
		fmt.Println("[aifei-gen] No tables found to generate.")
		return nil
	}

	// 2. Assign package/struct names
	for _, info := range tableInfos {
		tableName := info.Name
		if g.TablePrefix != "" {
			tableName = strings.TrimPrefix(tableName, g.TablePrefix)
		}
		info.PkgName = g.PkgNameFunc(tableName)
		info.StructName = g.StructNameFunc(tableName)
		info.BaseName = g.BaseNameFunc(info.StructName)
		fmt.Printf("[aifei-gen] Processing table: %s → package=%s struct=%s\n",
			info.Name, info.PkgName, info.StructName)
	}

	// 3. Generate per-table packages
	for _, info := range tableInfos {
		if err := g.baseGenerator.Generate(g.engine, info, g.outputDir); err != nil {
			return err
		}
		if err := g.modelGenerator.Generate(g.engine, info, g.outputDir); err != nil {
			return err
		}
		if err := g.daoGenerator.Generate(g.engine, info, g.outputDir); err != nil {
			return err
		}
		if err := g.serviceGenerator.Generate(g.engine, info, g.outputDir); err != nil {
			return err
		}
	}

	// 4. Generate tables.go
	outputPkgName := filepath.Base(g.outputDir)
	if err := g.tablesGenerator.Generate(g.engine, tableInfos, g.outputDir, g.importRoot, outputPkgName); err != nil {
		return err
	}

	fmt.Println("[aifei-gen] Code generation complete.")
	return nil
}
