package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/w32394045-dotcom/bg2x-chronos/internal/models"
)

// entryHash is the resolution result of one registry entry for
// `chronos models hash`.
type entryHash struct {
	ID         string   `json:"id"`
	Repo       string   `json:"repo,omitempty"`
	Revision   string   `json:"revision,omitempty"`
	Path       string   `json:"path,omitempty"`
	SHA256     string   `json:"sha256,omitempty"`
	Size       int64    `json:"size,omitempty"`
	Source     string   `json:"source,omitempty"` // lfs | download
	ExtraFiles []string `json:"extraFiles,omitempty"`
	Error      string   `json:"error,omitempty"`
}

// runModelsHash resolves the SHA-256 of every registry entry through the
// Hugging Face tree API (metadata only; model weights are never downloaded
// except for small non-LFS files when the fallback is enabled).
func runModelsHash(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("models hash", flag.ContinueOnError)
	registryPath := fs.String("registry", "", "path to registry.json (default: auto-detect)")
	jsonOut := fs.Bool("json", false, "print the resolution mapping as JSON")
	write := fs.Bool("write", false, "rewrite registry.json in place with the resolved sha256")
	check := fs.Bool("check", false, "exit non-zero if any registry entry has no valid sha256")
	apiBase := fs.String("api-base", "", "Hugging Face API base URL (default "+models.DefaultHFAPIBase+")")
	noDownload := fs.Bool("no-download", false, "never download small non-LFS files to compute their sha256")
	if err := fs.Parse(args); err != nil {
		return err
	}

	path, err := locateRegistry(*registryPath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	reg, err := models.ParseRegistry(data)
	if err != nil {
		return err
	}

	if !*check || *write {
		resolver := models.NewHFResolver()
		if *apiBase != "" {
			resolver.APIBase = *apiBase
		}
		if *noDownload {
			resolver.MaxFallbackDownload = 0
		}
		results := resolveRegistryHashes(ctx, reg, resolver)
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(map[string]any{"registry": path, "results": results}); err != nil {
				return err
			}
		} else {
			printHashResults(results)
		}
		if *write {
			patches := make(map[string]models.EntryPatch, len(results))
			for _, r := range results {
				if r.Error != "" || !models.ValidSHA256(r.SHA256) {
					continue
				}
				patch := models.EntryPatch{SHA256: r.SHA256}
				if len(r.ExtraFiles) > 0 {
					patch.ExtraFiles = r.ExtraFiles
				}
				patches[r.ID] = patch
			}
			changed, err := models.PatchRegistryFile(path, patches)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "patched %s (%d model(s) updated)\n", path, changed)
			// Re-read so --check sees the file that was just written.
			data, err = os.ReadFile(path)
			if err != nil {
				return err
			}
			reg, err = models.ParseRegistry(data)
			if err != nil {
				return err
			}
		}
	}

	if *check {
		if err := checkRegistryHashes(reg); err != nil {
			return err
		}
		if !*jsonOut {
			fmt.Printf("ok: %d registry entries have a valid sha256\n", len(reg.Models))
		}
	}
	return nil
}

// resolveRegistryHashes resolves every entry, recording per-entry failures
// instead of aborting the whole command.
func resolveRegistryHashes(ctx context.Context, reg *models.Registry, resolver *models.HFResolver) []entryHash {
	out := make([]entryHash, 0, len(reg.Models))
	for _, e := range reg.Models {
		h, err := resolver.Resolve(ctx, e)
		if err != nil {
			out = append(out, entryHash{ID: e.ID, Error: err.Error()})
			continue
		}
		out = append(out, entryHash{
			ID:         e.ID,
			Repo:       h.Repo,
			Revision:   h.Revision,
			Path:       h.Path,
			SHA256:     h.SHA256,
			Size:       h.Size,
			Source:     h.Source,
			ExtraFiles: h.ExtraFiles,
		})
	}
	return out
}

// printHashResults writes a human readable resolution table plus a summary.
func printHashResults(results []entryHash) {
	width := 2
	for _, r := range results {
		if len(r.ID) > width {
			width = len(r.ID)
		}
	}
	resolved, unresolved := 0, 0
	for _, r := range results {
		if r.Error != "" {
			unresolved++
			fmt.Printf("%-*s  UNRESOLVED  %s\n", width, r.ID, r.Error)
			continue
		}
		resolved++
		fmt.Printf("%-*s  %s  %8d  %-8s %s@%s/%s\n",
			width, r.ID, r.SHA256, r.Size, r.Source, r.Repo, r.Revision, r.Path)
	}
	fmt.Printf("\nresolved %d/%d", resolved, resolved+unresolved)
	if unresolved > 0 {
		fmt.Printf(" (%d unresolved)", unresolved)
	}
	fmt.Println()
}

// checkRegistryHashes fails when any entry lacks a valid SHA-256 digest.
func checkRegistryHashes(reg *models.Registry) error {
	var missing []string
	for _, e := range reg.Models {
		if !models.ValidSHA256(e.SHA256) {
			missing = append(missing, e.ID)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%d registry entr%s without a valid sha256: %s",
			len(missing), plural(len(missing), "y", "ies"), strings.Join(missing, ", "))
	}
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// locateRegistry finds registry.json: explicit flag, current directory, or the
// source tree of this command (useful for `go run`).
func locateRegistry(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	candidates := []string{filepath.Join("internal", "models", "registry.json")}
	if _, file, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates,
			filepath.Join(filepath.Dir(file), "..", "..", "internal", "models", "registry.json"))
	}
	for _, candidate := range candidates {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("registry.json not found (use --registry <path>)")
}
