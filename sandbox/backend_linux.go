// backend_linux.go linux bwrap 后端结构位（P5-s2 落地：
// bwrap --unshare-net + socat 桥接代理 + 挂载点清理 + ripgrep glob
// 展开 + seccomp）。
package sandbox

import "errors"

// ErrLinuxBackendPending linux bwrap 后端未移植（P5-s2）。
var ErrLinuxBackendPending = errors.New("sandbox: linux (bubblewrap) backend lands in P5-s2")
