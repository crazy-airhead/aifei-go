// util.go 扫描与路径小件（bleve 包内）。
package bleve

import (
	"os"
	"path/filepath"
	"strings"
)

func setOf(items ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(items))
	for _, it := range items {
		out[it] = struct{}{}
	}
	return out
}

func abs(p string) (string, error) { return filepath.Abs(p) }

// walkTextFiles 限深不限（Java walkFileTree 全量）：忽略目录整棵剪枝、
// 后缀白名单过滤（小写比对）；rel 为相对 root 的正斜杠路径。
func walkTextFiles(root string, ignore map[string]struct{}, exts map[string]struct{},
	visit func(rel, content string)) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 不可读路径静默跳过（Java visitFile throwable warn 同款）
		}
		name := d.Name()
		if d.IsDir() {
			if path != root {
				if _, skip := ignore[name]; skip {
					return filepath.SkipDir
				}
			}
			return nil
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
		if ext == "" {
			// 无后缀文件（如 .git/config 形态 root 下少见；Java lastDot==-1 → ext="" 不在白名单）
			return nil
		}
		if _, ok := exts[ext]; !ok {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil // 单文件失败跳过（Java LOG.warn 同款）
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		visit(filepath.ToSlash(rel), string(data))
		return nil
	})
}
