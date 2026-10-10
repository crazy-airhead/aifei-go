// util.go 工具名校验（Java ToolNameValidator）与 URI 模板匹配（RFC 6570
// 子集：{var} 单段 + {+var} 跨段，DefaultMcpUriTemplateManager 的常用面）。
package mcp

import (
	"regexp"
	"strings"
)

// validToolName 工具名规范（Java VALID_NAME_PATTERN）：1..128 位，仅
// A-Za-z0-9_-.
var toolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_\-.]{1,128}$`)

func validToolName(name string) bool { return toolNamePattern.MatchString(name) }

// matchURITemplate RFC 6570 子集匹配：模板含 {var}（匹配一段，不含 /）与
// {+var}（贪婪跨段）变量；uri 全串匹配成功时返回变量取值。
func matchURITemplate(tpl, uri string) (map[string]string, bool) {
	var sb strings.Builder
	sb.WriteString("^")
	var names []string
	for i := 0; i < len(tpl); {
		c := tpl[i]
		if c != '{' {
			sb.WriteString(regexp.QuoteMeta(string(c)))
			i++
			continue
		}
		end := strings.IndexByte(tpl[i:], '}')
		if end < 0 {
			return nil, false // 模板畸形：未闭合变量
		}
		expr := tpl[i+1 : i+end]
		i += end + 1
		name, pattern := expr, `[^/]+`
		if strings.HasPrefix(expr, "+") { // {+var}：保留扩展，可含 /
			name, pattern = expr[1:], `.+`
		}
		if name == "" {
			return nil, false
		}
		names = append(names, name)
		sb.WriteString("(" + pattern + ")")
	}
	sb.WriteString("$")
	re, err := regexp.Compile(sb.String())
	if err != nil {
		return nil, false
	}
	m := re.FindStringSubmatch(uri)
	if m == nil {
		return nil, false
	}
	vars := make(map[string]string, len(names))
	for idx, name := range names {
		vars[name] = m[idx+1]
	}
	return vars, true
}
