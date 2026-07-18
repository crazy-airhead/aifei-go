package enjoy

// Ctrl controls execution flow (break, continue, return).
type Ctrl struct {
	Break      bool
	Continue   bool
	Return     bool
	Wisdom     bool
	NullSafe   bool
	Attachment interface{}
	curLine    int // 当前执行到的 Stat 行号（渲染期 panic 定位用，对照 Java Location.row）
}

// NewCtrl creates a new Ctrl.
func NewCtrl() *Ctrl {
	return &Ctrl{}
}

// Reset clears all control flags.
func (c *Ctrl) Reset() {
	c.Break = false
	c.Continue = false
	c.Return = false
}
