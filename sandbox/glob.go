// glob.go glob 模式工具（对标 GlobUtils）。globToRegex 逐字移植
// sandbox-utils.ts：* 跨度不含 /、** 含、**/ 匹配零或多层目录、
// ? 单字符不含 /、[abc] 字符集；未闭合的 [ 按字面量转义。
package sandbox

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ContainsGlobChars 路径模式是否含 glob 字符。
func ContainsGlobChars(pathPattern string) bool {
	return strings.ContainsAny(pathPattern, "*?[]")
}

// RemoveTrailingGlobSuffix 剥离尾部 "/**"（子路径语义）；剥空返回 "/"
// （Java 同款——全 glob 输入 "**" 剥后为根）。
func RemoveTrailingGlobSuffix(pathPattern string) string {
	stripped := strings.TrimSuffix(pathPattern, "/**")
	if stripped == "" {
		return "/"
	}
	return stripped
}

// GlobToRegex glob 模式转正则（锚定 ^...$）。
func GlobToRegex(globPattern string) string {
	s := escapeRegexSpecials(globPattern)
	// 未闭合的 '[' 转义为字面量（Java replaceAll("\\[([^\\]]*?)$", "\\[$1")：
	// 找到第一个其后不含 ']' 的 '['）
	for i := 0; i < len(s); i++ {
		if s[i] == '[' && !strings.ContainsRune(s[i:], ']') {
			s = s[:i] + `\[` + s[i+1:]
			break
		}
	}
	// globstar 先占位再转单 *，避免 ** 被拆成两个 *
	s = strings.ReplaceAll(s, "**/", "__GLOBSTAR_SLASH__")
	s = strings.ReplaceAll(s, "**", "__GLOBSTAR__")
	s = strings.ReplaceAll(s, "*", "[^/]*")
	s = strings.ReplaceAll(s, "?", "[^/]")
	s = strings.ReplaceAll(s, "__GLOBSTAR_SLASH__", "(.*/)?")
	s = strings.ReplaceAll(s, "__GLOBSTAR__", ".*")
	return "^" + s + "$"
}

// CompileGlobRegex 编译 glob 正则。
func CompileGlobRegex(globPattern string) *regexp.Regexp {
	return regexp.MustCompile(GlobToRegex(globPattern))
}

// globRegexSpecials 需转义且不属于 glob 字符集的正则元字符
// （Java 字符类 [.^$+{}()|\\] 同款）。
const globRegexSpecials = ".^$+{}()|\\"

func escapeRegexSpecials(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		if strings.ContainsRune(globRegexSpecials, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ExpandGlobPattern 把 glob 模式展开为具体路径（linux bwrap 不支持
// glob，darwin 不需要——P5-s2 消费面）。步骤对标 Java：规范化 → 取
// 静态前缀 → 前缀为空或 "/" 视为过宽返回空 → 基目录不存在返回空 →
// 递归遍历按正则匹配（目录本身也参与匹配）。
func ExpandGlobPattern(globPath string) []string {
	normalizedPattern := NormalizePathForSandbox(globPath)

	// 静态前缀：首个 glob 字符之前
	staticPrefix := normalizedPattern
	if i := strings.IndexAny(normalizedPattern, "*?[]"); i >= 0 {
		staticPrefix = normalizedPattern[:i]
	}
	if staticPrefix == "" || staticPrefix == "/" {
		return nil
	}

	baseDir := staticPrefix
	if !strings.HasSuffix(baseDir, "/") {
		baseDir = filepath.Dir(baseDir)
	}
	if baseDir == "" || baseDir == "." {
		return nil
	}
	if fi, err := os.Stat(baseDir); err != nil || !fi.IsDir() {
		return nil
	}

	re := regexp.MustCompile(GlobToRegex(normalizedPattern))
	var results []string
	_ = filepath.Walk(baseDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 跳过不可访问项（Java visitFile 异常吞掉同款）
		}
		if p != baseDir && re.MatchString(p) {
			results = append(results, p)
		}
		return nil
	})
	return results
}
