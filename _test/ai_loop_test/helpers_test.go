// helpers_test.go 测试文件系统小件。
package ai_loop_test

import (
	"os"
	"path/filepath"
	"strings"
)

func osStat(p string) (os.FileInfo, error) { return os.Stat(p) }

func writeFile(p, content string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o600)
}

// countTmp .tmp 残留计数。
func countTmp(root string) int {
	count := 0
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.Contains(filepath.Base(p), ".tmp.") {
			count++
		}
		return nil
	})
	return count
}
