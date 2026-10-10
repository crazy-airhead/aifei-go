// talent_diff_test.go P5-t3 diff 能力包黑盒测试：apply_diff（上下文校验/
// 多 hunk 逆序应用/纯增纯删/围栏清洗/冲突文案/路径沙箱——业务失败为普通结
// 果串不标错）、apply_patch（Java 黄金用例：End Patch 标记不渗入内容、
// 天真应用算法的尾部追加怪癖、add/update/move/delete 三合一、验证失败错
// 误面、结果 map 结构）、管线端到端。
package ai_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/talent/diff"
)

func writeTestFile(t *testing.T, root, name, content string) string {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readTestFile(t *testing.T, root, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ---- apply_diff ----

func TestApplyDiffBasic(t *testing.T) {
	root := t.TempDir()
	d := diff.NewApplyDiff(root)
	tool := d.Tools(nil)[0]
	writeTestFile(t, root, "f.txt", "line1\nline2\nline3\n")

	patch := "--- f.txt\n+++ f.txt\n@@ -1,3 +1,3 @@\n line1\n-line2\n+LINE2\n line3\n"
	res := callFileTool(t, tool, map[string]any{"path": "f.txt", "diff": patch})
	if res.IsError || res.ContentText() != "成功: 补丁已应用到 f.txt" {
		t.Fatalf("apply_diff = %q err=%v", res.ContentText(), res.IsError)
	}
	if got := readTestFile(t, root, "f.txt"); got != "line1\nLINE2\nline3\n" {
		t.Fatalf("patched = %q", got)
	}
}

// 围栏包裹 + 缺 --- 头自动补造 + \ No newline 行忽略。
func TestApplyDiffFencedAndHeaderless(t *testing.T) {
	root := t.TempDir()
	tool := diff.NewApplyDiff(root).Tools(nil)[0]
	writeTestFile(t, root, "g.txt", "a\nb\n")

	patch := "```diff\n@@ -1,2 +1,2 @@\n a\n-b\n+B\n\\ No newline at end of file\n```"
	res := callFileTool(t, tool, map[string]any{"path": "g.txt", "diff": patch})
	if res.IsError {
		t.Fatalf("fenced = %q", res.ContentText())
	}
	if got := readTestFile(t, root, "g.txt"); got != "a\nB\n" {
		t.Fatalf("patched = %q", got)
	}
}

// 多 hunk 逆序应用：前位索引不受后位修改影响。
func TestApplyDiffMultiHunk(t *testing.T) {
	root := t.TempDir()
	tool := diff.NewApplyDiff(root).Tools(nil)[0]
	writeTestFile(t, root, "m.txt", "a\nb\nc\nd\ne\nf\n")

	patch := "@@ -1,2 +1,2 @@\n-a\n+A\n b\n@@ -5,2 +5,2 @@\n-e\n+E\n f\n"
	res := callFileTool(t, tool, map[string]any{"path": "m.txt", "diff": patch})
	if res.IsError {
		t.Fatalf("multi-hunk = %q", res.ContentText())
	}
	if got := readTestFile(t, root, "m.txt"); got != "A\nb\nc\nd\nE\nf\n" {
		t.Fatalf("patched = %q", got)
	}
}

func TestApplyDiffInsertDelete(t *testing.T) {
	root := t.TempDir()
	tool := diff.NewApplyDiff(root).Tools(nil)[0]

	// 纯插入：@@ -2,0 +3,1 @@ 在第 2 行之后插入
	writeTestFile(t, root, "i.txt", "l1\nl2\nl3\n")
	res := callFileTool(t, tool, map[string]any{"path": "i.txt",
		"diff": "@@ -2,0 +3,1 @@\n+NEW\n"})
	if res.IsError {
		t.Fatalf("insert = %q", res.ContentText())
	}
	if got := readTestFile(t, root, "i.txt"); got != "l1\nl2\nNEW\nl3\n" {
		t.Fatalf("inserted = %q", got)
	}

	// 纯删除：@@ -2,1 +2,0 @@ 删除第 2 行；计数缺省 =1（@@ -3 +3 @@ 形态）
	res = callFileTool(t, tool, map[string]any{"path": "i.txt",
		"diff": "@@ -2,1 +2,0 @@\n-l2\n"})
	if res.IsError {
		t.Fatalf("delete = %q", res.ContentText())
	}
	if got := readTestFile(t, root, "i.txt"); got != "l1\nNEW\nl3\n" {
		t.Fatalf("deleted = %q", got)
	}
}

// 冲突：文件已被改，上下文不匹配 → 冲突文案 + 文件原样。
func TestApplyDiffConflict(t *testing.T) {
	root := t.TempDir()
	tool := diff.NewApplyDiff(root).Tools(nil)[0]
	writeTestFile(t, root, "c.txt", "x\nCHANGED\ny\n")

	patch := "@@ -1,3 +1,3 @@\n x\n-old\n+new\n y\n"
	res := callFileTool(t, tool, map[string]any{"path": "c.txt", "diff": patch})
	if res.IsError {
		t.Fatalf("conflict must be a plain result, got err=%v", res.IsError)
	}
	if res.ContentText() != "应用补丁失败: 上下文不匹配（冲突）。请重新读取文件获取最新版本再生成 Diff。" {
		t.Fatalf("conflict = %q", res.ContentText())
	}
	if got := readTestFile(t, root, "c.txt"); got != "x\nCHANGED\ny\n" {
		t.Fatalf("file must stay unchanged, got %q", got)
	}
}

// 业务失败为普通结果串（Java 返回错误文案串而非异常）。
func TestApplyDiffErrors(t *testing.T) {
	root := t.TempDir()
	tool := diff.NewApplyDiff(root).Tools(nil)[0]

	res := callFileTool(t, tool, map[string]any{"path": "../escape.txt", "diff": "@@"})
	if res.IsError || res.ContentText() != "错误: 非法的路径访问。" {
		t.Fatalf("escape = %q err=%v", res.ContentText(), res.IsError)
	}
	res = callFileTool(t, tool, map[string]any{"path": "ghost.txt", "diff": "@@"})
	if res.IsError || res.ContentText() != "错误: 文件不存在: ghost.txt" {
		t.Fatalf("missing = %q err=%v", res.ContentText(), res.IsError)
	}
}

// ---- apply_patch ----

// Java 黄金用例搬译：*** End Patch 标记不得渗入新增文件内容。
func TestApplyPatchAddEndMarkerGolden(t *testing.T) {
	root := t.TempDir()
	tool := diff.NewApplyPatch(root).Tools(nil)[0]

	patch := "*** Begin Patch\n*** Add File: hello.txt\n+Hello\n*** End Patch\n"
	res := callFileTool(t, tool, map[string]any{"patchText": patch})
	if res.IsError {
		t.Fatalf("golden add = %v", res.ContentText())
	}
	if got := readTestFile(t, root, "hello.txt"); got != "Hello\n" {
		t.Fatalf("content = %q", got)
	}
}

// 天真应用算法（Java List.remove/add 同款）：删除=任意位置首个等值、新增=
// 尾部追加（含尾换行产生的空串元素之后）——怪癖逐字保留。
func TestApplyPatchUpdateNaive(t *testing.T) {
	root := t.TempDir()
	tool := diff.NewApplyPatch(root).Tools(nil)[0]
	writeTestFile(t, root, "u.txt", "keep\nremove\n")

	patch := "*** Begin Patch\n*** Update File: u.txt\n@@ ctx\n-remove\n+added\n*** End Patch\n"
	res := callFileTool(t, tool, map[string]any{"patchText": patch})
	if res.IsError {
		t.Fatalf("update = %v", res.ContentText())
	}
	// ["keep","remove",""] → remove "remove" → ["keep",""] → append "added" → "keep\n\nadded"
	if got := readTestFile(t, root, "u.txt"); got != "keep\n\nadded" {
		t.Fatalf("naive update = %q", got)
	}

	// 结果 map 结构（map 返回值经 JSON 序列化为文本）
	m := unmarshalToolResult(t, res)
	if m["output"] != "Success. Updated the following files:\nM u.txt" {
		t.Fatalf("output = %q", m["output"])
	}
	meta := m["metadata"].(map[string]any)
	if !strings.Contains(meta["diff"].(string), "@@") {
		t.Fatalf("metadata.diff = %q", meta["diff"])
	}
	files := meta["files"].([]any)
	f0 := files[0].(map[string]any)
	if f0["type"] != "update" || f0["additions"].(float64) != 1 || f0["deletions"].(float64) != 1 ||
		f0["relativePath"] != "u.txt" || f0["movePath"] != nil {
		t.Fatalf("file meta = %v", f0)
	}
	if f0["before"] != "keep\nremove\n" || f0["after"] != "keep\n\nadded" {
		t.Fatalf("before/after = %v / %v", f0["before"], f0["after"])
	}
	if _, hasDiag := meta["diagnostics"].(map[string]any); !hasDiag {
		t.Fatalf("diagnostics missing: %v", meta)
	}
}

// unmarshalToolResult 反解工具返回的 JSON map。
func unmarshalToolResult(t *testing.T, res *ai.ToolResult) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(res.ContentText()), &m); err != nil {
		t.Fatalf("result not JSON map: %v\n%s", err, res.ContentText())
	}
	return m
}

// add（建父目录）+ update move + delete 三合一。
func TestApplyPatchAddMoveDelete(t *testing.T) {
	root := t.TempDir()
	tool := diff.NewApplyPatch(root).Tools(nil)[0]
	writeTestFile(t, root, "old_name.txt", "v1\nv2\n")
	writeTestFile(t, root, "obsolete.txt", "bye\n")

	patch := "*** Begin Patch\n" +
		"*** Add File: sub/dir/new.txt\n+created\n" +
		"*** Update File: old_name.txt\n*** Move to: renamed.txt\n@@\n-v1\n+V1\n" +
		"*** Delete File: obsolete.txt\n" +
		"*** End Patch\n"
	res := callFileTool(t, tool, map[string]any{"patchText": patch})
	if res.IsError {
		t.Fatalf("combined = %v", res.ContentText())
	}

	if got := readTestFile(t, root, "sub/dir/new.txt"); got != "created\n" {
		t.Fatalf("added = %q", got)
	}
	// move：天真算法 "v1\nv2\n" → ["v1","v2",""] → -v1 → ["v2",""] → +V1 尾加 → "v2\n\nV1"
	if got := readTestFile(t, root, "renamed.txt"); got != "v2\n\nV1" {
		t.Fatalf("moved = %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "old_name.txt")); !os.IsNotExist(err) {
		t.Fatal("move must remove the source file")
	}
	if _, err := os.Stat(filepath.Join(root, "obsolete.txt")); !os.IsNotExist(err) {
		t.Fatal("delete must remove the file")
	}

	m := unmarshalToolResult(t, res)
	want := "Success. Updated the following files:\nA sub/dir/new.txt\nM renamed.txt\nD obsolete.txt"
	if m["output"] != want {
		t.Fatalf("output = %q", m["output"])
	}
	files := m["metadata"].(map[string]any)["files"].([]any)
	moveMeta := files[1].(map[string]any)
	if moveMeta["movePath"] == nil || moveMeta["type"] != "move" {
		t.Fatalf("move meta = %v", moveMeta)
	}
}

func TestApplyPatchErrors(t *testing.T) {
	root := t.TempDir()
	tool := diff.NewApplyPatch(root).Tools(nil)[0]

	// Go error → 管线加 "Error: " 前缀（Java RuntimeException 错误面的 Go 映射）
	cases := []struct {
		name      string
		patchText string
		want      string
	}{
		{"空参数", "", "Error: patchText is required"},
		{"空白参数", "  \n ", "Error: patchText is required"},
		{"空补丁", "*** Begin Patch\n*** End Patch", "Error: patch rejected: empty patch"},
		{"无区块", "random text\nno markers", "Error: apply_patch verification failed: no hunks found"},
	}
	for _, c := range cases {
		res := callFileTool(t, tool, map[string]any{"patchText": c.patchText})
		if !res.IsError || res.ContentText() != c.want {
			t.Errorf("%s = %q err=%v, want %q", c.name, res.ContentText(), res.IsError, c.want)
		}
	}

	// update 目标不存在 / 越权路径
	res := callFileTool(t, tool, map[string]any{"patchText": "*** Update File: ghost.txt\n+x\n*** End Patch\n"})
	if !res.IsError || !strings.Contains(res.ContentText(), "Failed to read file to update: ") {
		t.Fatalf("update-missing = %q err=%v", res.ContentText(), res.IsError)
	}
	res = callFileTool(t, tool, map[string]any{"patchText": "*** Update File: ../outside.txt\n+x\n*** End Patch\n"})
	if !res.IsError || !strings.Contains(res.ContentText(), "Access denied to ") {
		t.Fatalf("escape = %q err=%v", res.ContentText(), res.IsError)
	}
}

// ---- 管线端到端 ----

func TestDiffTalentPipeline(t *testing.T) {
	f := newFakeOpenAI(t, respTextJSON)
	m := fakeModel(t, f)
	root := t.TempDir()

	_, err := m.Prompt("打个补丁").Options(func(o *ai.ChatOptions) {
		o.Talents = []ai.Talent{diff.NewApplyDiff(root), diff.NewApplyPatch(root)}
	}).Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	body := f.request(t, 0).body
	tools, _ := body["tools"].([]any)
	got := map[string]bool{}
	for _, tv := range tools {
		tm, _ := tv.(map[string]any)
		got[tm["function"].(map[string]any)["name"].(string)] = true
	}
	if !got["apply_diff"] || !got["apply_patch"] {
		t.Fatalf("tools not deployed: %v", got)
	}

	// 纯工具集：无指令块
	msgs := f.messagesOf(t, 0)
	sysPrompt, _ := msgs[0]["content"].(string)
	if strings.Contains(sysPrompt, "# [Talent:") {
		t.Fatalf("pure toolsets must not emit instruction blocks:\n%s", sysPrompt)
	}

	opts := &ai.ChatOptions{}
	opts.Talents = []ai.Talent{diff.NewApplyDiff(root), diff.NewApplyPatch(root)}
	ai.ActiveTalents(opts, nil, new(strings.Builder))
	if len(opts.Tools) != 2 {
		t.Fatalf("deployed tools = %d", len(opts.Tools))
	}
	for _, tl := range opts.Tools {
		name, _ := tl.Meta()["talent"].(string)
		if name != "diff" && name != "patch" {
			t.Fatalf("tool %q meta.talent = %q", tl.Name(), name)
		}
	}
}
