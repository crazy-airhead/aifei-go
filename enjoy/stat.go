package enjoy

import "io"

// Stat is the interface for statement AST nodes.
type Stat interface {
	Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl)
}

// IOAdapter wraps io.Writer for template output.
type IOAdapter struct {
	w io.Writer
}

func (a *IOAdapter) Write(data []byte) (int, error) { return a.w.Write(data) }
func (a *IOAdapter) WriteString(s string) (int, error) {
	return a.w.Write([]byte(s))
}
