package artifact

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile 在 dir 下创建 rel（自动建父目录）并写入 n 个字节。
func writeFile(t *testing.T, dir, rel string, n int) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", n)), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDirSize 覆盖目录、文件、缺失路径与空路径。
func TestDirSize(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.bin", 100)
	writeFile(t, dir, "sub/b.bin", 250)
	writeFile(t, dir, "sub/deep/c.bin", 3)
	empty := t.TempDir()

	cases := []struct {
		name string
		path string
		want int64
	}{
		{"tree", dir, 353},
		{"nested_subdir", filepath.Join(dir, "sub"), 253},
		{"single_file", filepath.Join(dir, "a.bin"), 100},
		{"empty_dir", empty, 0},
		{"missing_path", filepath.Join(dir, "no-such-dir"), 0},
		{"empty_path", "", 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := DirSize(tc.path); got != tc.want {
				t.Fatalf("DirSize(%q) = %d, want %d", tc.path, got, tc.want)
			}
		})
	}
}

// TestRemove 覆盖文件、目录、缺失路径与危险路径。
func TestRemove(t *testing.T) {
	t.Run("removes_file", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "model.onnx", 10)
		if err := Remove(path); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("file still exists, stat err = %v", err)
		}
	})

	t.Run("removes_tree_and_keeps_siblings", func(t *testing.T) {
		dir := t.TempDir()
		modelDir := filepath.Join(dir, "real-esrgan-x4plus")
		writeFile(t, modelDir, "model.onnx", 500)
		writeFile(t, modelDir, "model.json", 60)
		keep := writeFile(t, dir, "other/model.onnx", 40)

		if err := Remove(modelDir); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if _, err := os.Stat(modelDir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("model dir still exists, stat err = %v", err)
		}
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("sibling removed: %v", err)
		}
	})

	t.Run("missing_path_is_ok", func(t *testing.T) {
		if err := Remove(filepath.Join(t.TempDir(), "nope", "deeper")); err != nil {
			t.Fatalf("Remove(missing) = %v, want nil", err)
		}
	})

	t.Run("dangerous_paths_refused", func(t *testing.T) {
		dir := t.TempDir()
		root := filepath.Clean(filepath.VolumeName(dir) + string(os.PathSeparator))
		paths := []string{"", "   ", ".", root}
		for _, path := range paths {
			if err := Remove(path); err == nil {
				t.Fatalf("Remove(%q) = nil, want refusal", path)
			}
		}
		// 拒绝后当前目录仍然存在。
		if _, err := os.Getwd(); err != nil {
			t.Fatalf("working directory vanished: %v", err)
		}
	})
}
