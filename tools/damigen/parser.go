package damigen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"strings"
)

// ifaceInfo describes a //dami:provider interface for template rendering.
type ifaceInfo struct {
	Name    string
	Topic   string
	Methods []methodInfo
}

// methodInfo describes one interface method for template rendering.
type methodInfo struct {
	IfaceName string
	Name      string
	Params    string // "(ctx context.Context, name string)"
	Results   string // "(int64, error)" or "error"
	CallExpr  string // "return dami.Call1[int64](c.Bus, ctx, \"m.Get\", name)"
}

// parseProviderComment extracts the topic mapping from a //dami:provider <topic>
// doc comment. Returns "" when absent.
func parseProviderComment(doc *ast.CommentGroup) string {
	if doc == nil {
		return ""
	}
	for _, c := range doc.List {
		text := strings.TrimPrefix(c.Text, "//")
		text = strings.TrimPrefix(text, "/*")
		text = strings.TrimSuffix(text, "*/")
		fields := strings.Fields(text)
		for i, f := range fields {
			if f == "dami:provider" && i+1 < len(fields) {
				return fields[i+1]
			}
		}
	}
	return ""
}

// parseMethods turns an interface's methods into methodInfo. It enforces the Go
// convention that each method returns (R, error) or error, and treats a leading
// context.Context parameter as the call's context (passed to dami.Call1/Call0
// rather than encoded as an arg).
func parseMethods(fset *token.FileSet, it *ast.InterfaceType, topic string) ([]methodInfo, error) {
	var methods []methodInfo
	for _, f := range it.Methods.List {
		if len(f.Names) == 0 {
			continue // embedded interface
		}
		ft, ok := f.Type.(*ast.FuncType)
		if !ok {
			continue
		}
		m, err := buildMethod(fset, f.Names[0].Name, ft, topic)
		if err != nil {
			return nil, err
		}
		methods = append(methods, m)
	}
	return methods, nil
}

func buildMethod(fset *token.FileSet, name string, ft *ast.FuncType, topic string) (methodInfo, error) {
	type param struct{ name, typ string }
	var ps []param
	ctxArg := "context.Background()" // default when there is no ctx parameter
	var argNames []string
	idx := 0
	if ft.Params != nil {
		for _, pf := range ft.Params.List {
			typ := exprString(fset, pf.Type)
			if len(pf.Names) == 0 {
				n := fmt.Sprintf("arg%d", idx)
				ps = append(ps, param{n, typ})
				if idx == 0 && typ == "context.Context" {
					ctxArg = n
				} else {
					argNames = append(argNames, n)
				}
				idx++
				continue
			}
			for _, nn := range pf.Names {
				ps = append(ps, param{nn.Name, typ})
				if idx == 0 && typ == "context.Context" {
					ctxArg = nn.Name
				} else {
					argNames = append(argNames, nn.Name)
				}
				idx++
			}
		}
	}
	var psParts []string
	for _, p := range ps {
		psParts = append(psParts, p.name+" "+p.typ)
	}
	paramsStr := "(" + strings.Join(psParts, ", ") + ")"

	var resTypes []string
	if ft.Results != nil {
		for _, rf := range ft.Results.List {
			typ := exprString(fset, rf.Type)
			n := 1
			if len(rf.Names) > 0 {
				n = len(rf.Names)
			}
			for i := 0; i < n; i++ {
				resTypes = append(resTypes, typ)
			}
		}
	}

	resultsStr, callExpr, err := buildCall(name, topic, resTypes, ctxArg, argNames)
	if err != nil {
		return methodInfo{}, err
	}
	return methodInfo{Name: name, Params: paramsStr, Results: resultsStr, CallExpr: callExpr}, nil
}

func buildCall(name, topic string, resTypes []string, ctxArg string, argNames []string) (resultsStr, callExpr string, err error) {
	topicFull := topic + "." + name
	argList := ""
	if len(argNames) > 0 {
		argList = ", " + strings.Join(argNames, ", ")
	}
	switch len(resTypes) {
	case 1:
		if resTypes[0] == "error" {
			return "error", fmt.Sprintf("return dami.Call0(c.Bus, %s, %q%s)", ctxArg, topicFull, argList), nil
		}
		return "", "", fmt.Errorf("method %s: single non-error result unsupported (use (R, error) or error)", name)
	case 2:
		if resTypes[1] != "error" {
			return "", "", fmt.Errorf("method %s: second result must be error", name)
		}
		r := resTypes[0]
		return "(" + r + ", error)", fmt.Sprintf("return dami.Call1[%s](c.Bus, %s, %q%s)", r, ctxArg, topicFull, argList), nil
	default:
		return "", "", fmt.Errorf("method %s: must return (R, error) or error", name)
	}
}

// exprString renders an ast.Expr back to source text via go/printer.
func exprString(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, fset, expr)
	return buf.String()
}
