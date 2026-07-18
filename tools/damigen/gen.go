// Package damigen generates strongly-typed consumer stubs for dami providers,
// the code-gen path that replaces Java's dynamic proxy (the default lpc consumer
// per the design). Given a package containing interfaces marked with
//
//	//dami:provider <topicMapping>
//	type XxxService interface { ... }
//
// Generate writes a <pkg>_dami.go (file name dami_gen.go) with an XxxClient that
// implements XxxService, delegating each method to dami.Call1/Call0.
//
// Templates are rendered with the Enjoy engine (github.com/crazy-airhead/
// aifei-go/enjoy) — the same engine used by tools/generator — giving the two
// codegen tools a unified template story. damigen itself depends only on the Go
// standard library plus enjoy.
package damigen

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/crazy-airhead/aifei-go/enjoy"
)

// DamiImportPath is the import path of the dami package referenced by generated
// clients. Exported so callers (e.g. a vendored dami) can override it.
var DamiImportPath = "github.com/crazy-airhead/aifei-go/dami"

const generatedFileName = "dami_gen.go"

// Generate scans srcDir for //dami:provider interfaces and writes a generated
// client file into outDir. It errors when no provider interface is found, or
// when a method does not match the supported (R, error)/error shape.
func Generate(srcDir, outDir string) error {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, srcDir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse %s: %w", srcDir, err)
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("no Go package found in %s", srcDir)
	}

	var pkgName string
	var pkg *ast.Package
	for name, p := range pkgs {
		pkgName, pkg = name, p
		break
	}

	ifaces, err := collectInterfaces(fset, pkg)
	if err != nil {
		return err
	}
	if len(ifaces) == 0 {
		return fmt.Errorf("no //dami:provider interfaces found in %s", srcDir)
	}

	code, err := render(pkgName, ifaces)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, generatedFileName), code, 0o644)
}

func collectInterfaces(fset *token.FileSet, pkg *ast.Package) ([]ifaceInfo, error) {
	var out []ifaceInfo
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				it, ok := ts.Type.(*ast.InterfaceType)
				if !ok {
					continue
				}
				doc := ts.Doc
				if doc == nil {
					doc = gd.Doc
				}
				topic := parseProviderComment(doc)
				if topic == "" {
					continue
				}
				methods, err := parseMethods(fset, it, topic)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", ts.Name.Name, err)
				}
				out = append(out, ifaceInfo{Name: ts.Name.Name, Topic: topic, Methods: methods})
			}
		}
	}
	return out, nil
}

// engine is the shared Enjoy engine for rendering client templates, mirroring
// tools/generator's approach (one engine, templates compiled from string).
var engine = enjoy.NewEngine("damigen")

func render(pkgName string, ifaces []ifaceInfo) ([]byte, error) {
	// Back-fill IfaceName on each method for the template.
	for i := range ifaces {
		for j := range ifaces[i].Methods {
			ifaces[i].Methods[j].IfaceName = ifaces[i].Name
		}
	}
	data := map[string]interface{}{
		"Package":    pkgName,
		"ImportPath": DamiImportPath,
		"Ifaces":     ifaces,
	}
	out, err := engine.GetTemplateByString(clientTemplate).RenderToString(data)
	if err != nil {
		return nil, fmt.Errorf("render client template: %w", err)
	}
	formatted, err := format.Source([]byte(out))
	if err != nil {
		// Return unformatted source so the caller can read the error context.
		return []byte(out), fmt.Errorf("format generated source: %w", err)
	}
	return formatted, nil
}
