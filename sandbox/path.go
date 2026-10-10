// path.go 路径工具（对标 SandboxPathUtils）：强制拒绝清单的静态
// 危险文件/目录表、系统缺省写路径、沙箱路径规范化（~/相对路径展开 +
// 符号链接边界防护）。
package sandbox

import (
	"os"
	"path/filepath"
	"strings"
)

// DangerousFiles 危险文件（shell 启动文件、git 配置、MCP 配置——
// 写入即代码执行面）。
var DangerousFiles = []string{
	".gitconfig", ".gitmodules", ".bashrc", ".bash_profile", ".bash_logout",
	".zshrc", ".zprofile", ".profile", ".ripgreprc", ".mcp.json",
}

// DangerousDirectories 危险目录（编辑器/工具配置——写入即影响宿主行为）。
var DangerousDirectories = []string{
	".git", ".vscode", ".idea",
}

// getDangerousDirectories 强制拒绝清单用的目录集（Java getDangerousDirectories
// 同款——.git 单列在 hooks/config 规则里，这里不含）。
func getDangerousDirectories() []string {
	return []string{".vscode", ".idea", ".claude/commands", ".claude/agents"}
}

// DefaultWritePaths 系统缺省写路径（设备文件、临时目录、Java/Go 临时
// 目录、npm/claude 日志目录）。任何写限制配置都以此为底。
func DefaultWritePaths() []string {
	home, _ := os.UserHomeDir()
	tmpDir := os.TempDir()

	paths := []string{
		// 设备文件
		"/dev/stdout", "/dev/stderr", "/dev/null", "/dev/tty",
		"/dev/dtracehelper", "/dev/autofs_nowait",
		// 通用临时目录（Linux/macOS）
		"/tmp", "/private/tmp",
		// macOS 特有临时目录
		"/var/folders", "/private/var/folders",
	}

	// 临时目录（macOS 下可能是 /var/folders/... 的符号链接形态）
	if tmpDir != "" && tmpDir != "." {
		normalizedTmpDir := tmpDir
		if !filepath.IsAbs(normalizedTmpDir) {
			if abs, err := filepath.Abs(normalizedTmpDir); err == nil {
				normalizedTmpDir = abs
			}
		}
		paths = append(paths, normalizedTmpDir)
		if strings.HasPrefix(normalizedTmpDir, "/var/") {
			paths = append(paths, "/private"+normalizedTmpDir)
		}
	}

	// 特定应用目录
	paths = append(paths, "/tmp/claude", "/private/tmp/claude")
	if home != "" {
		paths = append(paths, home+"/.npm/_logs", home+"/.claude/debug")
	}
	return paths
}

// NormalizeCaseForComparison 大小写不敏感比较用的归一（macOS 文件系统
// 大小写不敏感）。Java toLowerCase 同款（locale 无关）。
func NormalizeCaseForComparison(pathStr string) string {
	return strings.ToLower(pathStr)
}

// isSymlinkOutsideBoundary 符号链接是否逃出边界（原路径 vs 规范化路径）。
// 逐分支对标 Java isSymlinkOutsideBoundary：/tmp、/var 与其 /private
// 前缀形态互认、解析到根或单段即逃逸、原路径是解析结果的子路径即逃逸。
func isSymlinkOutsideBoundary(originalPath, resolvedPath string) bool {
	normalizedOriginal := filepath.Clean(originalPath)
	normalizedResolved := filepath.Clean(resolvedPath)

	if normalizedResolved == normalizedOriginal {
		return false
	}

	// macOS /tmp -> /private/tmp
	if strings.HasPrefix(normalizedOriginal, "/tmp/") &&
		normalizedResolved == "/private"+normalizedOriginal {
		return false
	}
	if strings.HasPrefix(normalizedOriginal, "/var/") &&
		normalizedResolved == "/private"+normalizedOriginal {
		return false
	}
	if strings.HasPrefix(normalizedOriginal, "/private/tmp/") &&
		normalizedResolved == normalizedOriginal {
		return false
	}
	if strings.HasPrefix(normalizedOriginal, "/private/var/") &&
		normalizedResolved == normalizedOriginal {
		return false
	}

	if normalizedResolved == "/" {
		return true
	}

	nonEmpty := 0
	for _, part := range strings.Split(normalizedResolved, "/") {
		if part != "" {
			nonEmpty++
		}
	}
	if nonEmpty <= 1 {
		return true
	}

	if strings.HasPrefix(normalizedOriginal, normalizedResolved+"/") {
		return true
	}

	// 规范形态复查（/tmp、/var 前缀补 /private）
	canonicalOriginal := normalizedOriginal
	if strings.HasPrefix(normalizedOriginal, "/tmp/") ||
		strings.HasPrefix(normalizedOriginal, "/var/") {
		canonicalOriginal = "/private" + normalizedOriginal
	}

	if canonicalOriginal != normalizedOriginal &&
		strings.HasPrefix(canonicalOriginal, normalizedResolved+"/") {
		return true
	}

	resolvedStartsWithOriginal := strings.HasPrefix(normalizedResolved, normalizedOriginal+"/")
	resolvedStartsWithCanonical := canonicalOriginal != normalizedOriginal &&
		strings.HasPrefix(normalizedResolved, canonicalOriginal+"/")
	resolvedIsCanonical := canonicalOriginal != normalizedOriginal &&
		normalizedResolved == canonicalOriginal
	resolvedIsSame := normalizedResolved == normalizedOriginal

	return !resolvedIsSame && !resolvedIsCanonical &&
		!resolvedStartsWithOriginal && !resolvedStartsWithCanonical
}

// NormalizePathForSandbox 沙箱用路径规范化：~ 与 ~/ 展开、./ 与 ../
// 及相对路径锚定 CWD；glob 模式只规范化静态基目录（保 glob 后缀）；
// 存在的路径解析符号链接（逃出边界的保持原样）。
func NormalizePathForSandbox(pathPattern string) string {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	normalizedPath := pathPattern

	switch {
	case pathPattern == "~":
		normalizedPath = home
	case strings.HasPrefix(pathPattern, "~/"):
		normalizedPath = home + pathPattern[1:]
	case strings.HasPrefix(pathPattern, "./") || strings.HasPrefix(pathPattern, "../"):
		normalizedPath = filepath.Join(cwd, pathPattern)
	case !filepath.IsAbs(pathPattern):
		normalizedPath = filepath.Join(cwd, pathPattern)
	}

	if ContainsGlobChars(normalizedPath) {
		// 只规范化 glob 的静态基目录，glob 后缀原样保留。baseDir 先剥尾
		// 部 "/"（Java substring(0, len-1) 同款）——后缀切片以未解析的
		// baseDir 长度为准，带斜杠切片会把基目录与 glob 段间的 "/" 吃掉。
		staticPrefix := normalizedPath
		if i := strings.IndexAny(normalizedPath, "*?[]"); i >= 0 {
			staticPrefix = normalizedPath[:i]
		}
		if staticPrefix != "" && staticPrefix != "/" {
			baseDir := staticPrefix
			if strings.HasSuffix(baseDir, "/") {
				baseDir = strings.TrimSuffix(baseDir, "/")
			} else {
				baseDir = filepath.Dir(baseDir)
			}
			if baseDir != "" && baseDir != "." {
				if resolvedBaseDir, err := filepath.EvalSymlinks(baseDir); err == nil {
					if !isSymlinkOutsideBoundary(baseDir, resolvedBaseDir) {
						return resolvedBaseDir + normalizedPath[len(baseDir):]
					}
				}
			}
		}
		return normalizedPath
	}

	if resolvedPath, err := filepath.EvalSymlinks(normalizedPath); err == nil {
		if !isSymlinkOutsideBoundary(normalizedPath, resolvedPath) {
			normalizedPath = resolvedPath
		}
	}
	return normalizedPath
}
