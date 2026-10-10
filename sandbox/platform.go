// platform.go 平台探测与依赖检查（对标 Platform/PlatformDetector/
// DependencyChecker/CommandLookup/SandboxDependencyCheck）。
package sandbox

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// Platform 运行平台。
type Platform int

const (
	MacOS Platform = iota
	Linux
	Windows
	Unknown
)

func (p Platform) String() string {
	switch p {
	case MacOS:
		return "macOS"
	case Linux:
		return "Linux"
	case Windows:
		return "Windows"
	default:
		return "unknown"
	}
}

var (
	platformOnce   sync.Once
	cachedPlatform Platform
	wslOnce        sync.Once
	cachedWslVer   string // "" = 未探测到 WSL
)

// DetectPlatform 探测当前平台（进程内缓存）。
func DetectPlatform() Platform {
	platformOnce.Do(func() {
		switch runtime.GOOS {
		case "darwin":
			cachedPlatform = MacOS
		case "linux":
			cachedPlatform = Linux
		case "windows":
			cachedPlatform = Windows
		default:
			cachedPlatform = Unknown
		}
	})
	return cachedPlatform
}

// WSLVersion linux 下读 /proc/version 探测 WSL 版本（"1"/"2"）；
// 非 WSL 返回 ""。WSL1 不支持沙箱（isSupportedPlatform 排除）。
func WSLVersion() string {
	wslOnce.Do(func() {
		if DetectPlatform() != Linux {
			return
		}
		content, err := os.ReadFile("/proc/version")
		if err != nil {
			return
		}
		s := string(content)
		if i := strings.Index(strings.ToLower(s), "wsl"); i >= 0 {
			rest := s[i+3:]
			if len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9' {
				cachedWslVer = rest[:1]
				return
			}
		}
		if strings.Contains(strings.ToLower(s), "microsoft") {
			cachedWslVer = "1"
		}
	})
	return cachedWslVer
}

// IsSupportedPlatform 当前平台是否支持沙箱（macOS/Windows/非 WSL1
// 的 Linux）。
func IsSupportedPlatform() bool {
	switch p := DetectPlatform(); p {
	case Linux:
		return WSLVersion() != "1"
	case MacOS, Windows:
		return true
	default:
		return false
	}
}

// ErrUnsupportedPlatform 平台不支持沙箱。
var ErrUnsupportedPlatform = errors.New("sandbox: configuration is not supported on this platform")

// ErrLinuxBackendPending / ErrWindowsBackendPending 后端结构位占位：
// linux bwrap 落地在 P5-s2，windows WFP 在 P5-s4（配置结构已就位，
// 包装分发提前给出明确错误面而非 panic）。
var (
	ErrLinuxBackendPending   = errors.New("sandbox: linux bwrap backend lands in P5-s2 (config structs already defined)")
	ErrWindowsBackendPending = errors.New("sandbox: windows WFP backend lands in P5-s4 (config structs already defined)")
)

// DependencyCheck 依赖检查结果（errors 致命、warnings 降级提示）。
type DependencyCheck struct {
	Errors   []string
	Warnings []string
}

// HasErrors 是否有致命缺失。
func (d *DependencyCheck) HasErrors() bool { return len(d.Errors) > 0 }

// CheckDependencies 按平台检查沙箱依赖（对标 DependencyChecker.check）：
// linux 需 ripgrep/bwrap/socat（seccomp 缺失仅告警）；macOS 无外部依赖
// （sandbox-exec 内建）；windows 在 P5-s4 后端落地时检查 srt-win.exe。
func CheckDependencies(cfg *RuntimeConfig) *DependencyCheck {
	if !IsSupportedPlatform() {
		return &DependencyCheck{Errors: []string{"Unsupported platform"}}
	}

	platform := DetectPlatform()
	var errs, warns []string

	if platform == Linux {
		// ripgrep
		rgCmd := "rg"
		if cfg != nil && cfg.Ripgrep != nil && cfg.Ripgrep.Command != "" {
			rgCmd = cfg.Ripgrep.Command
		}
		if _, err := exec.LookPath(rgCmd); err != nil {
			errs = append(errs, "ripgrep ("+rgCmd+") not found")
		}

		// bwrap
		bwrapPath := ""
		if cfg != nil {
			bwrapPath = cfg.BwrapPath
		}
		if bwrapPath != "" {
			if !isExecutableFile(bwrapPath) {
				errs = append(errs, "bubblewrap (bwrap) not executable at "+bwrapPath)
			}
		} else if _, err := exec.LookPath("bwrap"); err != nil {
			errs = append(errs, "bubblewrap (bwrap) not installed")
		}

		// socat
		socatPath := ""
		if cfg != nil {
			socatPath = cfg.SocatPath
		}
		if socatPath != "" {
			if !isExecutableFile(socatPath) {
				errs = append(errs, "socat not executable at "+socatPath)
			}
		} else if _, err := exec.LookPath("socat"); err != nil {
			errs = append(errs, "socat not installed")
		}

		// seccomp（仅告警）
		if cfg == nil || cfg.Seccomp == nil ||
			(cfg.Seccomp.ApplyPath == "" && cfg.Seccomp.Argv0 == "") {
			if cfg == nil || cfg.Seccomp == nil || cfg.Seccomp.Argv0 == "" {
				warns = append(warns, "seccomp not available - unix socket access not restricted")
			}
		}
	}

	return &DependencyCheck{Errors: errs, Warnings: warns}
}

// isExecutableFile 路径存在且可执行（Java isExecutable 同款）。
func isExecutableFile(path string) bool {
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}
