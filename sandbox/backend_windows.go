// backend_windows.go windows WFP 后端结构位（P5-s4 落地：srt-win.exe
// + Windows 组 + WFP 子层）。
package sandbox

import "errors"

// ErrWindowsBackendPending windows WFP 后端未移植（P5-s4）。
var ErrWindowsBackendPending = errors.New("sandbox: windows (WFP) backend lands in P5-s4")
