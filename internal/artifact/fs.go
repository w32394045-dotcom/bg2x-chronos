package artifact

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DirSize 返回 path 下所有普通文件的大小之和（字节），用于模型目录的
// 「占用空间」统计。path 不存在、不可读或为空时返回 0；不跟随符号链接，
// 单个子项读失败只跳过该子项。
func DirSize(path string) int64 {
	if strings.TrimSpace(path) == "" {
		return 0
	}
	var total int64
	// WalkDir 对不存在的根路径也会调用回调一次（带 err），回调返回 nil 即整体成功。
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// Remove 删除 path（文件或目录），用于模型卸载；路径不存在视为成功
// （幂等）。空路径、当前工作目录和文件系统根目录会被拒绝，避免误删。
func Remove(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("artifact: remove: empty path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("artifact: remove %s: %w", path, err)
	}
	clean := filepath.Clean(abs)
	if clean == filepath.Clean(filepath.VolumeName(clean)+string(os.PathSeparator)) {
		return fmt.Errorf("artifact: remove %s: refusing to remove filesystem root", path)
	}
	if wd, err := os.Getwd(); err == nil && strings.EqualFold(clean, filepath.Clean(wd)) {
		return fmt.Errorf("artifact: remove %s: refusing to remove the working directory", path)
	}
	if err := os.RemoveAll(clean); err != nil {
		return fmt.Errorf("artifact: remove %s: %w", path, err)
	}
	return nil
}
