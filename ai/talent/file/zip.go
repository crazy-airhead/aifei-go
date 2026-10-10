// zip.go 打包能力包（对标 solon-ai-talent-file:ZipTalent）。zip 工具把
// 混合路径（文件与目录）递归打包进沙箱内 zip 文件，条目名取相对根的
// POSIX 路径、目录带尾 "/"、防自包含（目标 zip 自身跳过——Java
// isSameFile 同款）；源路径不存在时静默跳过（Java 同款）。unzip 为 Go
// 增量（06 契约），解压条目逐一过沙箱判定防 zip-slip。
package file

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// Zip 打包能力包（zip/unzip 两工具，纯工具集形态）。
type Zip struct {
	ai.TalentBase
	root string
}

// NewZip 以 workDir 为沙箱根构造（不自动建目录——Java ZipTalent 同款，
// 与 ReadWrite 构造器的差异保留）。
func NewZip(workDir string) *Zip {
	return &Zip{root: absNorm(workDir)}
}

func (t *Zip) Name() string { return "zip" }

func (t *Zip) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{
		ai.NewTool("zip").Desc("将指定的路径列表（支持文件和目录混合）打包到 ZIP 文件中。").
			StringParam("zipFileName", "目标 zip 文件路径（相对沙箱根）", true).
			Param(ai.ParamSpec{Name: "paths", Type: "array", Required: true,
				Desc:  "源路径列表（相对沙箱根，文件与目录可混排）",
				Items: map[string]any{"type": "string"}}).
			Handler(t.doZip).Build(),
		ai.NewTool("unzip").Desc("将指定的 ZIP 文件解压到沙箱根目录下（条目路径逐一过沙箱校验，防 zip-slip）。").
			StringParam("zipFileName", "zip 文件路径（相对沙箱根）", true).
			Handler(t.doUnzip).Build(),
	}
}

// resolve 路径沙箱（同 ReadWrite 判定，越权文案随 Java ZipTalent）。
func (t *Zip) resolve(name string) (string, error) {
	return resolveUnder(t.root, name, "非法路径访问："+name)
}

func (t *Zip) doZip(_ context.Context, args map[string]any) (any, error) {
	zipFileName := argStr(args, "zipFileName")
	target, err := t.resolve(zipFileName)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, fmt.Errorf("打包失败: %v", err)
	}
	f, err := os.Create(target)
	if err != nil {
		return nil, fmt.Errorf("打包失败: %v", err)
	}
	w := zip.NewWriter(f)
	err = t.zipPaths(w, target, argStrSlice(args, "paths"))
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if ferr := f.Close(); err == nil {
		err = ferr
	}
	if err != nil {
		return nil, fmt.Errorf("打包失败: %v", err)
	}
	return "打包成功，保存至: " + zipFileName, nil
}

// zipPaths 逐源打包：目录递归（目录条目带尾 "/"，根相对名），文件单加。
func (t *Zip) zipPaths(w *zip.Writer, target string, paths []string) error {
	for _, name := range paths {
		src, err := t.resolve(name)
		if err != nil {
			return err
		}
		info, err := os.Stat(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue // 不存在静默跳过（Java 同款）
			}
			return err
		}
		if info.IsDir() {
			if err := filepath.WalkDir(src, func(path string, d fs.DirEntry, werr error) error {
				if werr != nil {
					return werr
				}
				entry, err := t.entryName(path)
				if err != nil {
					return err
				}
				if d.IsDir() {
					if entry == "" || entry == "." {
						return nil
					}
					_, err := w.Create(entry + "/")
					return err
				}
				return t.addFile(w, path, entry, info, target)
			}); err != nil {
				return err
			}
			continue
		}
		entry, err := t.entryName(src)
		if err != nil {
			return err
		}
		if err := t.addFile(w, src, entry, info, target); err != nil {
			return err
		}
	}
	return nil
}

// entryName 条目名 = 根相对 POSIX 路径（Java rootPath.relativize + 反斜杠
// 归一对应物）。
func (t *Zip) entryName(p string) (string, error) {
	rel, err := filepath.Rel(t.root, p)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

// addFile 单文件入包；目标 zip 自身跳过（防自包含锁死，Java isSameFile
// 同款——以已打开句柄比对）。
func (t *Zip) addFile(w *zip.Writer, path, entry string, info fs.FileInfo, target string) error {
	if path == target {
		return nil
	}
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	hdr := &zip.FileHeader{Name: entry, Method: zip.Deflate, Modified: info.ModTime()}
	dst, err := w.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	return err
}

func (t *Zip) doUnzip(_ context.Context, args map[string]any) (any, error) {
	zipFileName := argStr(args, "zipFileName")
	src, err := t.resolve(zipFileName)
	if err != nil {
		return nil, err
	}
	r, err := zip.OpenReader(src)
	if err != nil {
		return nil, fmt.Errorf("解压失败: %v", err)
	}
	defer r.Close()
	n := 0
	for _, f := range r.File {
		target, err := t.resolve(f.Name)
		if err != nil {
			return nil, fmt.Errorf("解压失败: 非法条目路径 %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return nil, fmt.Errorf("解压失败: %v", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, fmt.Errorf("解压失败: %v", err)
		}
		if err := copyZipEntry(f, target); err != nil {
			return nil, fmt.Errorf("解压失败: %v", err)
		}
		n++
	}
	return fmt.Sprintf("解压成功，共 %d 个文件: %s", n, zipFileName), nil
}

func copyZipEntry(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// absNorm 绝对化 + 归一（Java toAbsolutePath().normalize() 对应物）。
func absNorm(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return filepath.Clean(dir)
	}
	return abs
}

// resolveUnder 沙箱判定：根内（含根本身）放行，否则返回越权错误。
func resolveUnder(root, name, escapeMsg string) (string, error) {
	var p string
	if filepath.IsAbs(name) {
		p = filepath.Clean(name) // Java Path.resolve 绝对路径原样返回
	} else {
		p = filepath.Join(root, name)
	}
	if p != root && !strings.HasPrefix(p, root+string(filepath.Separator)) {
		return "", fmt.Errorf("%s", escapeMsg)
	}
	return p, nil
}

// argStr 取字符串参数（缺省空串）。
func argStr(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

// argStrSlice 取字符串数组参数（JSON 数组 []any 归一）。
func argStrSlice(args map[string]any, key string) []string {
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
