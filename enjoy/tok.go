package enjoy

// TokType represents template token types.
type TokType int

const (
	TokText TokType = iota
	TokOutput
	TokIf
	TokElseIf
	TokElse
	TokEnd
	TokFor
	TokSet
	TokSetLocal
	TokSetGlobal
	TokDefine
	TokInclude
	TokCall
	TokCallIfDefined
	TokSwitch
	TokCase
	TokDefault
	TokBreak
	TokContinue
	TokReturn
	TokReturnIf
	TokID
	TokEOF
)

// Token represents a template token.
type Token struct {
	Type TokType
	Val  string
	Name string // directive name when Type is TokID
	Line int    // token 所在行号（从 1 起），用于错误定位（对照 Java Location.row）
}
