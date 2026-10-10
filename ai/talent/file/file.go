// Package file 文件能力包（对标 solon-ai-talent-file：FileReadWriteTalent
// + ZipTalent 两 talent）。读写四工具带路径沙箱（根目录逃逸防护，词法
// 归一 + 前缀判定——Java resolvePath 同款，不解析符号链接）；zip 工具
// 打包混合路径并防自包含；unzip 为 Go 增量工具（Java 仅有打包；06 契约
// 列明 Unzip），解压侧带 zip-slip 防护。对 Java 的两处偏离：失败路径从
// 返回中文错误串改为 Go error（ai 管线统一加 "Error: " 前缀，文案逐字
// 保留）；charset 参数不迁（Go 侧统一 UTF-8，保 ai 模块零依赖）。
package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// DefaultMaxReadSize 读文件截断阈值（Java MAX_READ_SIZE = 24KB）。
const DefaultMaxReadSize = 24 * 1024

// ReadWrite 文本文件读写能力包（file_write/file_read/file_list/file_delete
// 四工具，纯工具集形态——无指令注入）。
type ReadWrite struct {
	ai.TalentBase
	root    string
	maxRead int
}

// NewReadWrite 以 workDir 为沙箱根构造。自动创建根目录，失败静默——错误
// 面收口在各工具执行时（Java 构造器吞 IOException 同款）。
func NewReadWrite(workDir string) *ReadWrite {
	root := absNorm(workDir)
	_ = os.MkdirAll(root, 0o755)
	return &ReadWrite{root: root, maxRead: DefaultMaxReadSize}
}

// MaxReadSize 覆写读截断阈值。
func (t *ReadWrite) MaxReadSize(n int) *ReadWrite {
	if n > 0 {
		t.maxRead = n
	}
	return t
}

func (t *ReadWrite) Name() string { return "file" }

func (t *ReadWrite) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{
		ai.NewTool("file_write").Desc("写入文本到文件。会自动创建不存在的目录。").
			StringParam("fileName", "相对沙箱根的文件路径", true).
			StringParam("content", "文本内容", true).
			Handler(t.write).Build(),
		ai.NewTool("file_read").Desc("读取文本文件内容。").
			StringParam("fileName", "相对沙箱根的文件路径", true).
			Handler(t.read).Build(),
		ai.NewTool("file_list").Desc("列出指定目录下的文件和子目录。如果不指定目录，则列出根目录。").
			StringParam("dirName", "相对沙箱根的目录路径（缺省根目录）", false).
			Handler(t.list).Build(),
		ai.NewTool("file_delete").Desc("删除指定文件或空目录。").
			StringParam("fileName", "相对沙箱根的文件路径", true).
			Handler(t.delete).Build(),
	}
}

// resolve 路径沙箱：Join 归一后必须仍在根内（Java resolvePath 同款词法
// 判定；绝对路径按 Java Path.resolve 语义原样保留——真在根内则放行、
// 否则越权拒绝，Go filepath.Join 会把绝对路径并入根下，语义不同故特判）。
func (t *ReadWrite) resolve(name string) (string, error) {
	return resolveUnder(t.root, name, "禁止越权访问沙箱外部")
}

func (t *ReadWrite) write(_ context.Context, args map[string]any) (any, error) {
	fileName := argStr(args, "fileName")
	p, err := t.resolve(fileName)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, fmt.Errorf("写入失败: %v", err)
	}
	content := argStr(args, "content") // null → ""（Java 同款）
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("写入失败: %v", err)
	}
	return "文件保存成功: " + fileName, nil
}

func (t *ReadWrite) read(_ context.Context, args map[string]any) (any, error) {
	fileName := argStr(args, "fileName")
	p, err := t.resolve(fileName)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("文件不存在: %s", fileName)
		}
		return nil, fmt.Errorf("读取失败: %v", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("读取失败：'%s' 是一个目录。请使用 file_list 查看其内容。", fileName)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("读取失败: %v", err)
	}
	if len(b) > t.maxRead {
		// 按字节截断（Java 同款——多字节字符可能被切半，靠替换符兜底）
		return string(b[:t.maxRead]) + fmt.Sprintf("\n...(内容过长，仅截取前 %dKB)...", t.maxRead/1024), nil
	}
	return string(b), nil
}

func (t *ReadWrite) list(_ context.Context, args map[string]any) (any, error) {
	dirName := argStr(args, "dirName")
	base := t.root
	if dirName != "" {
		p, err := t.resolve(dirName)
		if err != nil {
			return nil, err
		}
		base = p
	}
	info, err := os.Stat(base)
	if err != nil {
		if os.IsNotExist(err) {
			display := dirName
			if display == "" {
				display = "/"
			}
			return nil, fmt.Errorf("目录不存在: %s", display)
		}
		return nil, fmt.Errorf("获取列表失败: %v", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("路径不是目录: %s", dirName)
	}
	entries, err := os.ReadDir(base) // 深度 1、不含自身（Java Files.walk(dir,1) 同款）
	if err != nil {
		return nil, fmt.Errorf("获取列表失败: %v", err)
	}
	display := dirName
	if display == "" {
		display = "根目录"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "目录内容 (%s):\n", display)
	if len(entries) == 0 {
		sb.WriteString("空")
		return sb.String(), nil
	}
	for i, e := range entries {
		if i > 0 {
			sb.WriteByte('\n')
		}
		if e.IsDir() {
			sb.WriteString("[Dir] " + e.Name())
			continue
		}
		size := int64(0)
		if fi, err := e.Info(); err == nil {
			size = fi.Size()
		}
		fmt.Fprintf(&sb, "[File] %s (%d bytes)", e.Name(), size)
	}
	return sb.String(), nil
}

func (t *ReadWrite) delete(_ context.Context, args map[string]any) (any, error) {
	fileName := argStr(args, "fileName")
	p, err := t.resolve(fileName)
	if err != nil {
		return nil, err
	}
	err = os.Remove(p) // 文件或空目录（Java deleteIfExists 同款）
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("文件不存在。")
		}
		return nil, fmt.Errorf("删除失败: %v", err)
	}
	return "删除成功: " + fileName, nil
}
