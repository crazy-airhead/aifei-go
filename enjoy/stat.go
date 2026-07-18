package enjoy

import "io"

// Stat is the interface for statement AST nodes.
type Stat interface {
	Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl)
}

// nodeLoc 携带节点的源码行号（解析期从 Token.Line 填充），用于渲染期 panic 定位
// （对照 Java Location.row）。嵌入到 Stat struct 后，通过 lineSetter（写）/ locater（读）访问。
type nodeLoc struct{ line int }

// setLine 由解析器统一设置节点所在行（指针 receiver，使嵌入 nodeLoc 的 *Stat 满足 lineSetter）。
func (l *nodeLoc) setLine(line int) {
	if l != nil {
		l.line = line
	}
}

// Line 返回节点所在行（值 receiver，使嵌入 nodeLoc 的 Stat 满足 locater，供渲染期读取）。
func (l nodeLoc) Line() int { return l.line }

// locater 由持有源码行号的节点实现，渲染期跟踪「当前执行行」。Stat 接口不强制
// （db/sql 等外部 Stat 实现无需改造），用类型断言访问。
type locater interface{ Line() int }

// lineSetter 由持有可写行号的节点实现，解析器统一设行号。
type lineSetter interface{ setLine(int) }

// setStatLoc 若 stat 实现 lineSetter，设置其行号（对照 Java 节点构造时传入 Location）。
func setStatLoc(stat Stat, line int) {
	if stat == nil {
		return
	}
	if ls, ok := stat.(lineSetter); ok {
		ls.setLine(line)
	}
}

// curLine 读取 stat 所在行（未实现 locater 返回 0）。
func statLine(stat Stat) int {
	if l, ok := stat.(locater); ok {
		return l.Line()
	}
	return 0
}

// IOAdapter wraps io.Writer for template output.
type IOAdapter struct {
	w io.Writer
}

func (a *IOAdapter) Write(data []byte) (int, error) { return a.w.Write(data) }
func (a *IOAdapter) WriteString(s string) (int, error) {
	return a.w.Write([]byte(s))
}
