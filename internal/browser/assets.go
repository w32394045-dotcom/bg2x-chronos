package browser

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ResolveAssets finds the built frontend (index.html + assets/) for the headless
// CLI. The Vite output lives in frontend/dist during development and in web/
// inside packaged builds. An explicit path or CHRONOS_ASSETS wins.
func ResolveAssets(explicit string) (fs.FS, string, error) {
	candidates := make([]string, 0, 6)
	if explicit != "" {
		candidates = append(candidates, explicit)
	}
	if env := os.Getenv("CHRONOS_ASSETS"); env != "" {
		candidates = append(candidates, env)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "web"),
			filepath.Join(dir, "frontend", "dist"),
			filepath.Join(dir, "dist"),
		)
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(cwd, "frontend", "dist"),
			filepath.Join(cwd, "web"),
		)
	}
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if st, err := os.Stat(filepath.Join(dir, "index.html")); err == nil && !st.IsDir() {
			abs, err := filepath.Abs(dir)
			if err != nil {
				abs = dir
			}
			return os.DirFS(abs), abs, nil
		}
	}
	return nil, "", fmt.Errorf("chronos: UI assets not found; build the frontend (cd frontend && pnpm build) or set CHRONOS_ASSETS/--assets")
}
