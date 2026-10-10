// diff.go Unified Diff 应用能力包（对标 solon-ai-talent-diff:ApplyDiffTalent）。
// java-diff-utils 的 parseUnifiedDiff/applyTo 在 Go 侧零依赖自写：hunk 头
// 解析（@@ -a[,n] +b[,m] @@）+ 逆序按位应用（后位先行，前位索引稳定），
// 逐行校验上下文与删除行——不匹配即冲突文案（Java PatchFailedException
// 同款结果面）。Java 返回错误文案串（非异常）的业务失败，Go 保持普通结
// 果串不标错（与 file 包「Java 抛异常→Go error」的映射规则互补）。
package diff

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

var (
	fenceHeadRe = regexp.MustCompile("^```[a-zA-Z]*\\s+")
	fenceTailRe = regexp.MustCompile("\\s+```$")
	hunkHeadRe  = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
)

// ApplyDiff Unified Diff 补丁应用能力包（apply_diff 工具，纯工具集形态）。
type ApplyDiff struct {
	ai.TalentBase
	root string
}

// NewApplyDiff 以 workDir 为工作目录构造（Java 的 __cwd 注入在 Go 侧固化为
// 构造期工作目录）。
func NewApplyDiff(workDir string) *ApplyDiff {
	return &ApplyDiff{root: absNorm(workDir)}
}

func (t *ApplyDiff) Name() string { return "diff" }

func (t *ApplyDiff) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{
		ai.NewTool("apply_diff").
			Desc("使用标准的 Unified Diff 补丁精准修改现有文件。当你需要对已有代码进行局部微调时，这是首选工具。\n"+
				"它具有以下特性：\n"+
				"1. 上下文感知：通过 @@ 块周围的行进行定位，即使行号微调也能成功。\n"+
				"2. 冲突检测：如果文件已被他人修改导致上下文不匹配，会报错提醒。\n"+
				"3. 节省 Token：无需发送整个文件，仅发送变化的部分。").
			StringParam("path", "相对于工作目录的文件路径", true).
			StringParam("diff", "Unified Diff 格式的补丁内容 (包含 @@ 块和上下文)", true).
			Handler(t.apply).Build(),
	}
}

func (t *ApplyDiff) apply(_ context.Context, args map[string]any) (any, error) {
	if t.root == "" {
		return "错误: 未找到工作目录上下文。", nil
	}
	path := argStr(args, "path")
	target, err := resolveUnder(t.root, path)
	if err != nil {
		return "错误: 非法的路径访问。", nil
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return "错误: 文件不存在: " + path, nil
		}
		return "应用补丁失败: 文件读写错误: " + err.Error(), nil
	}

	original := readAllLines(raw)
	diffLines := prepareDiffLines(path, argStr(args, "diff"))

	hunks, err := parseUnifiedDiff(diffLines)
	if err != nil {
		return "应用补丁失败: 内部错误: " + err.Error(), nil
	}
	patched, err := applyHunks(original, hunks)
	if err != nil {
		return "应用补丁失败: 上下文不匹配（冲突）。请重新读取文件获取最新版本再生成 Diff。", nil
	}
	out := strings.Join(patched, "\n") + "\n"
	if err := os.WriteFile(target, []byte(out), 0o644); err != nil {
		return "应用补丁失败: 文件读写错误: " + err.Error(), nil
	}
	return "成功: 补丁已应用到 " + path, nil
}

// prepareDiffLines 补丁行清洗（Java 同名逐字移植）：剥 Markdown 围栏、缺
// --- 头时补造、过滤围栏行与 \ 起始的 Git 辅助行。
func prepareDiffLines(path, diffText string) []string {
	clean := strings.TrimSpace(diffText)
	if strings.HasPrefix(clean, "```") {
		clean = fenceHeadRe.ReplaceAllString(clean, "")
		clean = fenceTailRe.ReplaceAllString(clean, "")
	}
	lines := splitLines(clean)

	hasHeader := false
	for _, line := range lines {
		if strings.HasPrefix(line, "---") {
			hasHeader = true
			break
		}
	}
	var result []string
	if !hasHeader {
		result = append(result, "--- "+path, "+++ "+path)
	}
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") || strings.HasPrefix(line, "\\") {
			continue
		}
		result = append(result, line)
	}
	return result
}

// uHunk 单个 @@ 块（old/new 行均含上下文，已剥前缀）。
type uHunk struct {
	oldStart, oldCount int
	newStart, newCount int
	oldLines, newLines []string
}

// parseUnifiedDiff 解析 @@ 块序列（头两行 ---/+++ 跳过；' '/'-'/'+' 归位；
// '\\' 行忽略；计数满足即块收口）。
func parseUnifiedDiff(lines []string) ([]uHunk, error) {
	var hunks []uHunk
	var cur *uHunk
	for _, line := range lines {
		if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") {
			continue
		}
		if m := hunkHeadRe.FindStringSubmatch(line); m != nil {
			if cur != nil {
				return nil, fmt.Errorf("malformed diff: 未收口的 @@ 块")
			}
			cur = &uHunk{
				oldStart: atoi(m[1]), oldCount: countOrOne(m[2]),
				newStart: atoi(m[3]), newCount: countOrOne(m[4]),
			}
			continue
		}
		if cur == nil {
			continue // 头部噪声行
		}
		switch {
		case line == "" || strings.HasPrefix(line, " "):
			cur.oldLines = append(cur.oldLines, strings.TrimPrefix(line, " "))
			cur.newLines = append(cur.newLines, strings.TrimPrefix(line, " "))
		case strings.HasPrefix(line, "-"):
			cur.oldLines = append(cur.oldLines, line[1:])
		case strings.HasPrefix(line, "+"):
			cur.newLines = append(cur.newLines, line[1:])
		case strings.HasPrefix(line, "\\"):
			// "\ No newline at end of file"：忽略
		default:
			return nil, fmt.Errorf("malformed diff: 无法识别的补丁行 %q", line)
		}
		if len(cur.oldLines) == cur.oldCount && len(cur.newLines) == cur.newCount {
			hunks = append(hunks, *cur)
			cur = nil
		}
	}
	if cur != nil {
		return nil, fmt.Errorf("malformed diff: 块行数不足（期望 -%d,%d +%d,%d）",
			cur.oldStart, cur.oldCount, cur.newStart, cur.newCount)
	}
	return hunks, nil
}

// applyHunks 逆序应用（后位先行——前位索引稳定，java-diff-utils applyTo
// 同款）；逐行校验旧跨度（上下文+删除行）与目标一致，不符即冲突。
func applyHunks(orig []string, hunks []uHunk) ([]string, error) {
	for i := len(hunks) - 1; i >= 0; i-- {
		h := hunks[i]
		pos := h.oldStart - 1
		if h.oldCount == 0 {
			pos = h.oldStart // 空旧跨度：头值即 0-based 插入点（"第 N 行之后"）
		}
		if pos < 0 || pos > len(orig) || pos+h.oldCount > len(orig) {
			return nil, fmt.Errorf("hunk out of range")
		}
		for j := 0; j < h.oldCount; j++ {
			if orig[pos+j] != h.oldLines[j] {
				return nil, fmt.Errorf("context mismatch at line %d", pos+j+1)
			}
		}
		spliced := make([]string, 0, len(orig)-h.oldCount+len(h.newLines))
		spliced = append(spliced, orig[:pos]...)
		spliced = append(spliced, h.newLines...)
		spliced = append(spliced, orig[pos+h.oldCount:]...)
		orig = spliced
	}
	return orig, nil
}

// ---- 共用小件 ----

// readAllLines Java Files.readAllLines 对应物：按行切分并去掉尾换行产生的
// 末位空串（回写时统一补尾换行）。
func readAllLines(b []byte) []string {
	lines := splitLines(string(b))
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// splitLines \R 语义（\r\n 归一后按 \n 切，保留空行——Java split limit -1）。
func splitLines(s string) []string {
	if s == "" {
		return []string{""}
	}
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

// absNorm 绝对化 + 归一。
func absNorm(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return filepath.Clean(dir)
	}
	return abs
}

// resolveUnder 路径沙箱（Java workPath.resolve(path).normalize() + startsWith
// 同款；绝对路径按 Java Path.resolve 语义原样保留特判）。
func resolveUnder(root, name string) (string, error) {
	var p string
	if filepath.IsAbs(name) {
		p = filepath.Clean(name)
	} else {
		p = filepath.Join(root, name)
	}
	if p != root && !strings.HasPrefix(p, root+string(filepath.Separator)) {
		return "", fmt.Errorf("路径越权")
	}
	return p, nil
}

// argStr 取字符串参数（缺省空串）。
func argStr(args map[string]any, key string) string {
	if s, ok := args[key].(string); ok {
		return s
	}
	return ""
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func countOrOne(s string) int {
	if s == "" {
		return 1
	}
	return atoi(s)
}
