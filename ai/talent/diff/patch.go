// patch.go OpenCode apply_patch 应用能力包（对标 solon-ai-talent-diff:
// ApplyPatchTalent——其本身为 OpenCode 工具的 Java 移植）。两阶段执行：先
// 解析全部区块并预演变更（add/update/delete/move + LCS 统计 + 统一 diff
// 摘要），后统一落盘。update 的补丁应用是刻意的天真算法（删除行=任意位
// 置首个等值删除、新增行=尾部追加——Java List.remove/add 同款，逐字保
// 留）；diff 生成零依赖自写（前后缀裁剪 + DP LCS，超限降级为单块粗粒
// 度）。Java 的 RuntimeException 验证失败面 → Go error（管线标错）。
package diff

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// ApplyPatch OpenCode 补丁应用能力包（apply_patch 工具，纯工具集形态）。
type ApplyPatch struct {
	ai.TalentBase
	root string
}

// NewApplyPatch 以 workDir 为工作目录构造。
func NewApplyPatch(workDir string) *ApplyPatch {
	return &ApplyPatch{root: absNorm(workDir)}
}

func (t *ApplyPatch) Name() string { return "patch" }

// patchDesc Java @ToolMapping description 逐字移植。
const patchDesc = "使用 `apply_patch` 工具来编辑文件。补丁语言是一种简化的、面向文件的 diff 格式，设计目的是易于解析且安全。你可以将其视为一个高级封装：\n" +
	"\n" +
	"*** Begin Patch\n" +
	"[ 一个或多个文件区块 ]\n" +
	"*** End Patch\n" +
	"\n" +
	"在此封装内，是一系列文件操作序列。\n" +
	"你必须包含一个头部（Header）来指定你要执行的操作。\n" +
	"每个操作以以下三种头部之一开始：\n" +
	"\n" +
	"*** Add File: <path> - 创建新文件。后续每一行都必须是以 + 开头的行（初始内容）。\n" +
	"*** Delete File: <path> - 删除现有文件。下方无需跟任何内容。\n" +
	"*** Update File: <path> - 就地修补现有文件（可选重命名操作）。\n" +
	"\n" +
	"补丁示例：\n" +
	"\n" +
	"```\n" +
	"*** Begin Patch\n" +
	"*** Add File: hello.txt\n" +
	"+Hello world\n" +
	"*** Update File: src/app.py\n" +
	"*** Move to: src/main.py\n" +
	"@@ def greet():\n" +
	"-print(\"Hi\")\n" +
	"+print(\"Hello, world!\")\n" +
	"*** Delete File: obsolete.txt\n" +
	"*** End Patch\n" +
	"```\n" +
	"\n" +
	"请务必记住：\n" +
	"\n" +
	"- 你必须包含一个指明意图的操作头部（Add/Delete/Update）。\n" +
	"- 即使在创建新文件时，新行也必须以 `+` 为前缀。"

func (t *ApplyPatch) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{
		ai.NewTool("apply_patch").
			Desc(patchDesc).
			StringParam("patchText", "The full patch text that describes all changes to be made", true).
			Handler(t.apply).Build(),
	}
}

func (t *ApplyPatch) apply(_ context.Context, args map[string]any) (any, error) {
	if t.root == "" {
		return nil, fmt.Errorf("错误: 未找到工作目录上下文。")
	}
	patchText := argStr(args, "patchText")
	if strings.TrimSpace(patchText) == "" {
		return nil, fmt.Errorf("patchText is required")
	}
	hunks := parsePatchText(patchText)
	if len(hunks) == 0 {
		normalized := strings.TrimSpace(strings.ReplaceAll(
			strings.ReplaceAll(patchText, "\r\n", "\n"), "\r", "\n"))
		if normalized == "*** Begin Patch\n*** End Patch" {
			return nil, fmt.Errorf("patch rejected: empty patch")
		}
		return nil, fmt.Errorf("apply_patch verification failed: no hunks found")
	}

	changes := make([]*fileChange, 0, len(hunks))
	var totalDiff strings.Builder
	for _, hunk := range hunks {
		filePath, err := resolveUnder(t.root, hunk.path)
		if err != nil {
			return nil, fmt.Errorf("apply_patch verification failed: Access denied to %s",
				filepath.Join(t.root, hunk.path))
		}
		change := &fileChange{filePath: filePath}
		switch hunk.typ {
		case "add":
			change.typ = "add"
			change.oldContent = ""
			change.newContent = hunk.contents
			if change.newContent != "" && !strings.HasSuffix(change.newContent, "\n") {
				change.newContent += "\n"
			}
			change.calculateDiffAndStats()
		case "update":
			info, err := os.Stat(filePath)
			if err != nil || info.IsDir() {
				return nil, fmt.Errorf("apply_patch verification failed: Failed to read file to update: %s", filePath)
			}
			if change.oldContent, err = readFile(filePath); err != nil {
				return nil, fmt.Errorf("apply_patch verification failed: %v", err)
			}
			if change.newContent, err = applyUpdateHunks(change.oldContent, hunk.lines); err != nil {
				return nil, fmt.Errorf("apply_patch verification failed: %v", err)
			}
			if hunk.movePath != "" {
				change.movePath, err = resolveUnder(t.root, hunk.movePath)
				if err != nil {
					return nil, fmt.Errorf("apply_patch verification failed: Access denied to %s",
						filepath.Join(t.root, hunk.movePath))
				}
				change.typ = "move"
			} else {
				change.typ = "update"
			}
			change.calculateDiffAndStats()
		case "delete":
			if change.oldContent, err = readFile(filePath); err != nil {
				return nil, fmt.Errorf("apply_patch verification failed: %v", err)
			}
			change.typ = "delete"
			change.newContent = ""
			change.calculateDiffAndStats()
		}
		changes = append(changes, change)
		totalDiff.WriteString(change.diffText)
		totalDiff.WriteString("\n")
	}

	// 两阶段：全部预演成功后统一落盘
	for _, change := range changes {
		if err := change.applyToDisk(); err != nil {
			return nil, err
		}
	}

	var summary []string
	for _, c := range changes {
		base := c.filePath
		if c.movePath != "" {
			base = c.movePath
		}
		rel := filepath.ToSlash(relTo(t.root, base))
		switch c.typ {
		case "add":
			summary = append(summary, "A "+rel)
		case "delete":
			summary = append(summary, "D "+rel)
		default:
			summary = append(summary, "M "+rel)
		}
	}
	output := "Success. Updated the following files:\n" + strings.Join(summary, "\n")

	files := make([]map[string]any, 0, len(changes))
	for _, c := range changes {
		base := c.filePath
		if c.movePath != "" {
			base = c.movePath
		}
		var movePath any
		if c.movePath != "" {
			movePath = c.movePath
		}
		files = append(files, map[string]any{
			"filePath":     c.filePath,
			"relativePath": filepath.ToSlash(relTo(t.root, base)),
			"type":         c.typ,
			"diff":         c.diffText,
			"before":       c.oldContent,
			"after":        c.newContent,
			"additions":    c.additions,
			"deletions":    c.deletions,
			"movePath":     movePath,
		})
	}
	return map[string]any{
		"title": output,
		"metadata": map[string]any{
			"diff":        totalDiff.String(),
			"files":       files,
			"diagnostics": map[string]any{},
		},
		"output": output,
	}, nil
}

// ---- 解析与应用 ----

// patchHunk 单文件区块（Java PatchHunk）。
type patchHunk struct {
	typ      string // add / update / delete
	path     string
	movePath string
	contents string
	lines    []string
}

// parsePatchText 前缀宽度（*** Add File: = 13 等）与行归集规则逐字对标
// Java；*** End Patch 置空 current 防标记渗入后续区块内容。
func parsePatchText(text string) []*patchHunk {
	var hunks []*patchHunk
	var current *patchHunk
	for _, line := range splitLines(text) {
		switch {
		case strings.HasPrefix(line, "*** Add File:"):
			current = &patchHunk{typ: "add", path: strings.TrimSpace(line[13:])}
			hunks = append(hunks, current)
		case strings.HasPrefix(line, "*** Update File:"):
			current = &patchHunk{typ: "update", path: strings.TrimSpace(line[16:])}
			hunks = append(hunks, current)
		case strings.HasPrefix(line, "*** Delete File:"):
			current = &patchHunk{typ: "delete", path: strings.TrimSpace(line[16:])}
			hunks = append(hunks, current)
		case line == "*** Begin Patch":
			// 封装头：跳过
		case line == "*** End Patch":
			current = nil
		case strings.HasPrefix(line, "*** Move to:") && current != nil:
			current.movePath = strings.TrimSpace(line[12:])
		case current != nil:
			if current.typ == "add" {
				content := line
				if strings.HasPrefix(line, "+") {
					content = line[1:]
				}
				current.contents += content + "\n"
			} else if !strings.HasPrefix(line, "@@") {
				current.lines = append(current.lines, line)
			}
		}
	}
	return hunks
}

// applyUpdateHunks 天真应用（Java applyHunks 逐字对应）：删除行=任意位置
// 首个等值删除（不存在则静默跳过），新增行=尾部追加；行间 \n 连接无尾换行。
func applyUpdateHunks(oldContent string, diffLines []string) (string, error) {
	lines := splitLines(oldContent)
	for _, d := range diffLines {
		switch {
		case strings.HasPrefix(d, "-"):
			target := d[1:]
			for i, l := range lines {
				if l == target {
					lines = append(lines[:i], lines[i+1:]...)
					break
				}
			}
		case strings.HasPrefix(d, "+"):
			lines = append(lines, d[1:])
		}
	}
	return strings.Join(lines, "\n"), nil
}

// ---- 变更与落盘 ----

// fileChange 单文件变更（Java FileChange）。
type fileChange struct {
	filePath, movePath   string
	oldContent           string
	newContent           string
	typ                  string // add / update / delete / move
	diffText             string
	additions, deletions int
}

// calculateDiffAndStats LCS 统计 + 统一 diff 摘要（上下文 0，仅 @@ 块）。
// delete 的删除数走整文行数捷径（Java 同款），不走 delta 循环。
func (c *fileChange) calculateDiffAndStats() {
	oldLines := splitLines(c.oldContent)
	newLines := splitLines(c.newContent)
	if c.typ == "delete" && c.oldContent != "" {
		c.deletions = len(splitLines(c.oldContent))
	}
	if c.typ != "delete" {
		for _, d := range diffLineDeltas(oldLines, newLines) {
			c.additions += d.insCount
			c.deletions += d.delCount
		}
	}
	var sb strings.Builder
	for _, h := range unifiedHunks(oldLines, newLines) {
		sb.WriteString(h.header + "\n")
		for _, l := range h.oldPart {
			sb.WriteString("-" + l + "\n")
		}
		for _, l := range h.newPart {
			sb.WriteString("+" + l + "\n")
		}
	}
	c.diffText = sb.String()
}

// applyToDisk 物理落盘（delete 删源；move 写目标删源；其余写目标）。
func (c *fileChange) applyToDisk() error {
	target := c.filePath
	if c.movePath != "" {
		target = c.movePath
	}
	if c.typ == "delete" {
		if err := os.Remove(c.filePath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if dir := filepath.Dir(target); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(target, []byte(c.newContent), 0o644); err != nil {
		return err
	}
	if c.typ == "move" {
		if err := os.Remove(c.filePath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// readFile UTF-8 读全量（缺文件报 OS 原文——Java NoSuchFileException.toString 对应物）。
func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// relTo 根相对路径（Java Path.relativize 对应物；调用方保证 root 前缀）。
func relTo(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return rel
}

// ---- 零依赖 LCS diff ----

// lineDelta 连续差异段（相邻删+增合并为一处变更——unified diff 惯例）。
type lineDelta struct {
	delStart, delCount int // 旧序列 0-based
	insStart, insCount int // 新序列 0-based
}

// dpCellCap DP 表上限（约 1000x1000；超过降级单块粗粒度——整文件重写
// 场景，行数统计仍准确，仅 +/- 分块粒度变粗）。
const dpCellCap = 1_000_000

// diffLineDeltas 行级差异：先裁公共前后缀（LLM 补丁中段极小），中段走
// DP LCS 回溯成差异段。
func diffLineDeltas(oldLines, newLines []string) []lineDelta {
	n, m := len(oldLines), len(newLines)
	// 公共前缀
	p := 0
	for p < n && p < m && oldLines[p] == newLines[p] {
		p++
	}
	// 公共后缀（不与前缀重叠）
	s := 0
	for s < n-p && s < m-p && oldLines[n-1-s] == newLines[m-1-s] {
		s++
	}
	mo := oldLines[p : n-s]
	mn := newLines[p : m-s]
	if len(mo) == 0 && len(mn) == 0 {
		return nil
	}
	if len(mo)*len(mn) > dpCellCap {
		return []lineDelta{{p, len(mo), p, len(mn)}} // 粗粒度降级
	}
	// dp[i][j] = mo[i:], mn[j:] 的 LCS 长度
	dp := make([][]int32, len(mo)+1)
	for i := range dp {
		dp[i] = make([]int32, len(mn)+1)
	}
	for i := len(mo) - 1; i >= 0; i-- {
		for j := len(mn) - 1; j >= 0; j-- {
			if mo[i] == mn[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	// 前向回溯成差异段
	var deltas []lineDelta
	i, j := 0, 0
	flush := func(delStart, delCount, insStart, insCount int) {
		if delCount > 0 || insCount > 0 {
			deltas = append(deltas, lineDelta{delStart, delCount, insStart, insCount})
		}
	}
	var dStart, dCount, iStart, iCount int
	inRun := false
	for i < len(mo) || j < len(mn) {
		switch {
		case i < len(mo) && j < len(mn) && mo[i] == mn[j]:
			flush(p+dStart, dCount, p+iStart, iCount)
			inRun, dCount, iCount = false, 0, 0
			i++
			j++
		case j == len(mn) || (i < len(mo) && dp[i+1][j] >= dp[i][j+1]):
			if !inRun {
				dStart, iStart, inRun = i, j, true
			}
			dCount++
			i++
		default:
			if !inRun {
				dStart, iStart, inRun = i, j, true
			}
			iCount++
			j++
		}
	}
	flush(p+dStart, dCount, p+iStart, iCount)
	return deltas
}

// unifiedHunk @@ 块生成（上下文 0）。
type unifiedHunk struct {
	header  string
	oldPart []string
	newPart []string
}

// unifiedHunks 差异段 → @@ 块（-a[,0] +b[,0] 计数规则：空跨度时头值=行数
// 而非行号——git 惯例）。行序先 - 后 +（CHANGE 型）。
func unifiedHunks(oldLines, newLines []string) []unifiedHunk {
	deltas := diffLineDeltas(oldLines, newLines)
	hunks := make([]unifiedHunk, 0, len(deltas))
	for _, d := range deltas {
		oldStart, newStart := d.delStart+1, d.insStart+1
		if d.delCount == 0 {
			oldStart = d.delStart
		}
		if d.insCount == 0 {
			newStart = d.insStart
		}
		hunks = append(hunks, unifiedHunk{
			header:  fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldStart, d.delCount, newStart, d.insCount),
			oldPart: oldLines[d.delStart : d.delStart+d.delCount],
			newPart: newLines[d.insStart : d.insStart+d.insCount],
		})
	}
	return hunks
}
