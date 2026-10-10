// talent_file_test.go P5-t1 file 能力包黑盒测试：读写四工具（成功路径/
// 错误文案/24KB 截断/路径沙箱逃逸）、zip 混合打包（目录条目/防自包含/
// 缺源跳过）、unzip（roundtrip + zip-slip 防护）、经 ai 管线挂载的端到端
// （工具部署 + 纯工具集无指令块 + meta 染色）。
package ai_test

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/talent/file"
)

// fileTool 按名取 talent 工具。
func fileTool(t *testing.T, tools []ai.FunctionTool, name string) ai.FunctionTool {
	t.Helper()
	for _, tl := range tools {
		if tl.Name() == name {
			return tl
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func callFileTool(t *testing.T, tl ai.FunctionTool, args map[string]any) *ai.ToolResult {
	t.Helper()
	return tl.Call(context.Background(), args)
}

// ---- 读写四工具 ----

func TestFileReadWriteTools(t *testing.T) {
	root := t.TempDir()
	rw := file.NewReadWrite(root)
	if rw.Name() != "file" {
		t.Fatalf("Name = %q", rw.Name())
	}
	tools := rw.Tools(nil)
	if len(tools) != 4 {
		t.Fatalf("tools = %d, want 4", len(tools))
	}

	// 写：自动建父目录 + 成功文案
	res := callFileTool(t, fileTool(t, tools, "file_write"), map[string]any{
		"fileName": "notes/a/hello.txt", "content": "你好，file",
	})
	if res.IsError || res.ContentText() != "文件保存成功: notes/a/hello.txt" {
		t.Fatalf("write = %v err=%v", res.ContentText(), res.IsError)
	}
	if b, err := os.ReadFile(filepath.Join(root, "notes/a/hello.txt")); err != nil || string(b) != "你好，file" {
		t.Fatalf("on-disk = %q err=%v", b, err)
	}

	// 读：内容往返
	res = callFileTool(t, fileTool(t, tools, "file_read"), map[string]any{"fileName": "notes/a/hello.txt"})
	if res.IsError || res.ContentText() != "你好，file" {
		t.Fatalf("read = %q err=%v", res.ContentText(), res.IsError)
	}

	// 读：不存在 / 目录
	res = callFileTool(t, fileTool(t, tools, "file_read"), map[string]any{"fileName": "ghost.txt"})
	if !res.IsError || !strings.Contains(res.ContentText(), "文件不存在: ghost.txt") {
		t.Fatalf("read-missing = %q err=%v", res.ContentText(), res.IsError)
	}
	res = callFileTool(t, fileTool(t, tools, "file_read"), map[string]any{"fileName": "notes"})
	if !res.IsError || !strings.Contains(res.ContentText(), "是一个目录") ||
		!strings.Contains(res.ContentText(), "file_list") {
		t.Fatalf("read-dir = %q err=%v", res.ContentText(), res.IsError)
	}

	// 读：>24KB 截断 + 标记
	big := strings.Repeat("a", 25*1024)
	res = callFileTool(t, fileTool(t, tools, "file_write"), map[string]any{"fileName": "big.txt", "content": big})
	if res.IsError {
		t.Fatalf("write-big = %v", res.ContentText())
	}
	res = callFileTool(t, fileTool(t, tools, "file_read"), map[string]any{"fileName": "big.txt"})
	if res.IsError {
		t.Fatalf("read-big err = %v", res.ContentText())
	}
	if !strings.Contains(res.ContentText(), "...(内容过长，仅截取前 24KB)...") {
		t.Fatalf("truncation marker missing: %q", res.ContentText())
	}
	if n := len(res.ContentText()); n > 24*1024+64 {
		t.Fatalf("truncated read len = %d", n)
	}

	// 列表：头 + [Dir]/[File] + 字节数
	res = callFileTool(t, fileTool(t, tools, "file_list"), map[string]any{})
	if res.IsError {
		t.Fatalf("list = %v", res.ContentText())
	}
	listed := res.ContentText()
	if !strings.HasPrefix(listed, "目录内容 (根目录):") {
		t.Fatalf("list header = %q", listed)
	}
	if !strings.Contains(listed, "[Dir] notes") || !strings.Contains(listed, "[File] big.txt (25600 bytes)") {
		t.Fatalf("list entries = %q", listed)
	}
	res = callFileTool(t, fileTool(t, tools, "file_list"), map[string]any{"dirName": "notes/a"})
	if res.IsError || !strings.HasPrefix(res.ContentText(), "目录内容 (notes/a):") ||
		!strings.Contains(res.ContentText(), "[File] hello.txt") {
		t.Fatalf("list-sub = %q err=%v", res.ContentText(), res.IsError)
	}
	res = callFileTool(t, fileTool(t, tools, "file_list"), map[string]any{"dirName": "ghost"})
	if !res.IsError || !strings.Contains(res.ContentText(), "目录不存在: ghost") {
		t.Fatalf("list-missing = %q err=%v", res.ContentText(), res.IsError)
	}
	res = callFileTool(t, fileTool(t, tools, "file_list"), map[string]any{"dirName": "big.txt"})
	if !res.IsError || !strings.Contains(res.ContentText(), "路径不是目录: big.txt") {
		t.Fatalf("list-file = %q err=%v", res.ContentText(), res.IsError)
	}

	// 删除：成功 + 再删报不存在
	res = callFileTool(t, fileTool(t, tools, "file_delete"), map[string]any{"fileName": "big.txt"})
	if res.IsError || res.ContentText() != "删除成功: big.txt" {
		t.Fatalf("delete = %q err=%v", res.ContentText(), res.IsError)
	}
	res = callFileTool(t, fileTool(t, tools, "file_delete"), map[string]any{"fileName": "big.txt"})
	if !res.IsError || !strings.Contains(res.ContentText(), "文件不存在。") {
		t.Fatalf("delete-again = %q err=%v", res.ContentText(), res.IsError)
	}
}

// 路径沙箱：越权路径拒绝；绝对路径经 Join 被并入根下（不逃逸）。
func TestFileReadWriteSandbox(t *testing.T) {
	root := t.TempDir()
	rw := file.NewReadWrite(root)
	tools := rw.Tools(nil)

	for _, escape := range []string{"../escape.txt", "a/../../escape.txt", "sub/../../../escape.txt"} {
		res := callFileTool(t, fileTool(t, tools, "file_write"), map[string]any{
			"fileName": escape, "content": "x",
		})
		if !res.IsError || !strings.Contains(res.ContentText(), "禁止越权访问沙箱外部") {
			t.Fatalf("escape %q = %q err=%v", escape, res.ContentText(), res.IsError)
		}
	}

	// 绝对路径：根内放行（Java Path.resolve 绝对路径原样返回 + 前缀判定）
	res := callFileTool(t, fileTool(t, tools, "file_write"), map[string]any{
		"fileName": filepath.Join(root, "abs.txt"), "content": "abs",
	})
	if res.IsError {
		t.Fatalf("abs-under-root write = %v", res.ContentText())
	}
	if b, err := os.ReadFile(filepath.Join(root, "abs.txt")); err != nil || string(b) != "abs" {
		t.Fatalf("abs.txt = %q err=%v", b, err)
	}
	// 绝对路径：根外拒绝
	res = callFileTool(t, fileTool(t, tools, "file_read"), map[string]any{
		"fileName": filepath.Join(os.TempDir(), "outside-secret.txt"),
	})
	if !res.IsError || !strings.Contains(res.ContentText(), "禁止越权访问沙箱外部") {
		t.Fatalf("abs-outside = %q err=%v", res.ContentText(), res.IsError)
	}
}

// ---- zip / unzip ----

func TestFileZipTalent(t *testing.T) {
	root := t.TempDir()
	z := file.NewZip(root)
	if z.Name() != "zip" {
		t.Fatalf("Name = %q", z.Name())
	}
	tools := z.Tools(nil)
	if len(tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(tools))
	}

	// 备料：目录（含嵌套）+ 单文件 + 不存在的源
	os.MkdirAll(filepath.Join(root, "docs/deep"), 0o755)
	os.WriteFile(filepath.Join(root, "docs/readme.md"), []byte("readme"), 0o644)
	os.WriteFile(filepath.Join(root, "docs/deep/a.txt"), []byte("deep"), 0o644)
	os.WriteFile(filepath.Join(root, "solo.txt"), []byte("solo"), 0o644)

	res := callFileTool(t, fileTool(t, tools, "zip"), map[string]any{
		"zipFileName": "out.zip",
		"paths":       []any{"docs", "solo.txt", "ghost-dir"},
	})
	if res.IsError || res.ContentText() != "打包成功，保存至: out.zip" {
		t.Fatalf("zip = %q err=%v", res.ContentText(), res.IsError)
	}

	r, err := zip.OpenReader(filepath.Join(root, "out.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	names := map[string]string{} // name → content
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			names[f.Name] = "<dir>"
			continue
		}
		rc, _ := f.Open()
		buf := make([]byte, f.UncompressedSize64)
		n, _ := rc.Read(buf)
		rc.Close()
		names[f.Name] = string(buf[:n])
	}
	for _, want := range []string{"docs/", "docs/readme.md", "docs/deep/", "docs/deep/a.txt", "solo.txt"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("entry %q missing in %v", want, names)
		}
	}
	if names["docs/readme.md"] != "readme" || names["solo.txt"] != "solo" {
		t.Fatalf("contents = %v", names)
	}

	// 防自包含：把已存在的 out.zip 自身列为源 → 条目不重复入包
	res = callFileTool(t, fileTool(t, tools, "zip"), map[string]any{
		"zipFileName": "out2.zip",
		"paths":       []any{"out2.zip", "solo.txt"},
	})
	if res.IsError {
		t.Fatalf("zip-self = %v", res.ContentText())
	}
	r2, err := zip.OpenReader(filepath.Join(root, "out2.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	if len(r2.File) != 1 || r2.File[0].Name != "solo.txt" {
		t.Fatalf("self-containment guard failed: %d entries", len(r2.File))
	}

	// 沙箱：越权 zip 目标
	res = callFileTool(t, fileTool(t, tools, "zip"), map[string]any{
		"zipFileName": "../out.zip", "paths": []any{"solo.txt"},
	})
	if !res.IsError || !strings.Contains(res.ContentText(), "非法路径访问") {
		t.Fatalf("zip-escape = %q err=%v", res.ContentText(), res.IsError)
	}
}

func TestFileUnzipTalent(t *testing.T) {
	root := t.TempDir()
	z := file.NewZip(root)
	tools := z.Tools(nil)

	// 备 zip：目录 + 文件
	os.MkdirAll(filepath.Join(root, "src/d"), 0o755)
	os.WriteFile(filepath.Join(root, "src/d/x.txt"), []byte("X"), 0o644)
	os.WriteFile(filepath.Join(root, "src/y.txt"), []byte("Y"), 0o644)
	if res := callFileTool(t, fileTool(t, tools, "zip"), map[string]any{
		"zipFileName": "pkg.zip", "paths": []any{"src"},
	}); res.IsError {
		t.Fatalf("zip = %v", res.ContentText())
	}
	os.RemoveAll(filepath.Join(root, "src"))

	res := callFileTool(t, fileTool(t, tools, "unzip"), map[string]any{"zipFileName": "pkg.zip"})
	if res.IsError || !strings.Contains(res.ContentText(), "解压成功") ||
		!strings.Contains(res.ContentText(), "2 个文件") {
		t.Fatalf("unzip = %q err=%v", res.ContentText(), res.IsError)
	}
	if b, err := os.ReadFile(filepath.Join(root, "src/d/x.txt")); err != nil || string(b) != "X" {
		t.Fatalf("x.txt = %q err=%v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "src/y.txt")); err != nil || string(b) != "Y" {
		t.Fatalf("y.txt = %q err=%v", b, err)
	}
}

// zip-slip：恶意条目 ../evil.txt 必须被拒（Go 增量工具的沙箱防线）。
func TestFileUnzipZipSlip(t *testing.T) {
	root := t.TempDir()
	z := file.NewZip(root)

	evil := filepath.Join(root, "evil.zip")
	f, err := os.Create(evil)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	ef, _ := w.Create("../evil.txt")
	ef.Write([]byte("pwn"))
	w.Close()
	f.Close()

	res := callFileTool(t, z.Tools(nil)[1], map[string]any{"zipFileName": "evil.zip"})
	if !res.IsError || !strings.Contains(res.ContentText(), "非法条目路径") {
		t.Fatalf("zip-slip = %q err=%v", res.ContentText(), res.IsError)
	}
	if _, err := os.Stat(filepath.Join(root, "..", "evil.txt")); !os.IsNotExist(err) {
		t.Fatal("evil.txt must not escape the sandbox")
	}
}

// ---- 管线端到端：挂 ChatOptions.Talents → 工具部署 + 无指令块 ----

func TestFileTalentPipeline(t *testing.T) {
	f := newFakeOpenAI(t, respTextJSON)
	m := fakeModel(t, f)
	root := t.TempDir()

	_, err := m.Prompt("整理文件").Options(func(o *ai.ChatOptions) {
		o.Talents = []ai.Talent{file.NewReadWrite(root), file.NewZip(root)}
	}).Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	// 请求 0 的工具表：六工具全部部署给模型
	body := f.request(t, 0).body
	tools, _ := body["tools"].([]any)
	got := map[string]bool{}
	for _, tv := range tools {
		tm, _ := tv.(map[string]any)
		got[tm["function"].(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"file_write", "file_read", "file_list", "file_delete", "zip", "unzip"} {
		if !got[want] {
			t.Fatalf("tool %q not deployed: %v", want, got)
		}
	}

	// 纯工具集：系统提示无 talent 指令块
	msgs := f.messagesOf(t, 0)
	sys, _ := msgs[0]["content"].(string)
	if strings.Contains(sys, "# [Talent:") {
		t.Fatalf("pure toolset must not emit instruction block:\n%s", sys)
	}

	// meta 染色：ActiveTalents 部署后 opts.Tools 带 talent=<name>
	opts := &ai.ChatOptions{}
	opts.Talents = []ai.Talent{file.NewReadWrite(root), file.NewZip(root)}
	ai.ActiveTalents(opts, nil, new(strings.Builder))
	if len(opts.Tools) != 6 {
		t.Fatalf("deployed tools = %d", len(opts.Tools))
	}
	for _, tl := range opts.Tools {
		name, _ := tl.Meta()["talent"].(string)
		if name != "file" && name != "zip" {
			t.Fatalf("tool %q meta.talent = %q", tl.Name(), name)
		}
	}
}
