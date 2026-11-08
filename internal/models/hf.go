package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Hugging Face helpers. They resolve the SHA-256 of a registry file from the HF
// tree API (lfs.oid) without downloading model weights.

const (
	// DefaultHFAPIBase is the public Hugging Face endpoint.
	DefaultHFAPIBase = "https://huggingface.co"
	// DefaultMaxFallbackHashSize is the largest non-LFS file the hash command
	// may download to compute a SHA-256 locally.
	DefaultMaxFallbackHashSize int64 = 20 << 20
	// maxTreePages bounds tree API pagination.
	maxTreePages = 20
)

// ErrHFNoChecksum reports that a file is not LFS-managed, so the tree API
// exposes no SHA-256 and the hash must be computed from the bytes (if allowed).
var ErrHFNoChecksum = errors.New("chronos: hf: file is not LFS-managed; no sha256 in the tree api")

// HFTarget is one file of a Hugging Face model repository.
type HFTarget struct {
	Repo     string `json:"repo"`
	Revision string `json:"revision"`
	Path     string `json:"path"`
	URL      string `json:"url"`
}

// HFLFS is the LFS metadata of a tree entry.
type HFLFS struct {
	OID         string `json:"oid"`
	Size        int64  `json:"size"`
	PointerSize int64  `json:"pointerSize"`
}

// HFTreeFile is one entry of the Hugging Face tree API response.
type HFTreeFile struct {
	Type    string `json:"type"`
	Path    string `json:"path"`
	OID     string `json:"oid"`
	Size    int64  `json:"size"`
	LFS     *HFLFS `json:"lfs,omitempty"`
	XetHash string `json:"xetHash,omitempty"`
}

// HFEntryHash is the resolved checksum of a registry entry.
type HFEntryHash struct {
	ID         string   `json:"id"`
	Repo       string   `json:"repo"`
	Revision   string   `json:"revision"`
	Path       string   `json:"path"`
	SHA256     string   `json:"sha256"`
	Size       int64    `json:"size"`
	Source     string   `json:"source"`               // "lfs" (tree api) | "download" (small non-LFS fallback)
	Companions []string `json:"companions,omitempty"` // repo-relative sibling files (e.g. <path>.data)
	ExtraFiles []string `json:"extraFiles,omitempty"` // local names next to the weights file
}

// ParseHFResolveURL parses a Hugging Face download URL of the shape
// https://huggingface.co/<owner>/<repo>/resolve/<revision>/<path>.
func ParseHFResolveURL(raw string) (HFTarget, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return HFTarget{}, fmt.Errorf("chronos: hf: parse url %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return HFTarget{}, fmt.Errorf("chronos: hf: unsupported url scheme in %q", raw)
	}
	host := strings.ToLower(u.Hostname())
	if host != "huggingface.co" && host != "www.huggingface.co" {
		return HFTarget{}, fmt.Errorf("chronos: hf: not a huggingface.co url: %q", raw)
	}
	p, err := url.PathUnescape(u.Path)
	if err != nil {
		return HFTarget{}, fmt.Errorf("chronos: hf: unescape url path %q: %w", raw, err)
	}
	segs := splitURLPath(p)
	if len(segs) < 5 || (segs[2] != "resolve" && segs[2] != "blob") {
		return HFTarget{}, fmt.Errorf("chronos: hf: want /<owner>/<repo>/resolve/<revision>/<path>, got %q", raw)
	}
	repo := segs[0] + "/" + segs[1]
	revision := segs[3]
	filePath := strings.Join(segs[4:], "/")
	if repo == "" || revision == "" || filePath == "" {
		return HFTarget{}, fmt.Errorf("chronos: hf: incomplete resolve url %q", raw)
	}
	return HFTarget{Repo: repo, Revision: revision, Path: filePath, URL: raw}, nil
}

func splitURLPath(p string) []string {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

// MatchHFTree finds target.Path in a tree listing and returns its LFS SHA-256.
// It returns ErrHFNoChecksum (with the hash partially filled) for files that are
// not stored in LFS.
func MatchHFTree(target HFTarget, tree []HFTreeFile) (HFEntryHash, error) {
	h := HFEntryHash{Repo: target.Repo, Revision: target.Revision, Path: target.Path}
	var hit *HFTreeFile
	for i := range tree {
		if tree[i].Type != "" && tree[i].Type != "file" {
			continue
		}
		if tree[i].Path == target.Path {
			hit = &tree[i]
			continue
		}
		if tree[i].Path == target.Path+DataSuffix {
			h.Companions = append(h.Companions, tree[i].Path)
		}
	}
	if hit == nil {
		return h, fmt.Errorf("chronos: hf: %s not found in %s@%s", target.Path, target.Repo, target.Revision)
	}
	h.Size = hit.Size
	if hit.LFS == nil || hit.LFS.OID == "" {
		return h, fmt.Errorf("%w: %s", ErrHFNoChecksum, target.Path)
	}
	if !ValidSHA256(hit.LFS.OID) {
		return h, fmt.Errorf("chronos: hf: %s has malformed lfs.oid %q", target.Path, hit.LFS.OID)
	}
	h.SHA256 = strings.ToLower(hit.LFS.OID)
	if hit.LFS.Size > 0 {
		h.Size = hit.LFS.Size
	}
	h.Source = "lfs"
	return h, nil
}

// LocalExtraFiles maps repo-relative companion paths to the local file names
// stored next to the primary weights file (e.g. "model.onnx.data").
func LocalExtraFiles(primaryName, weightsPath string, companions []string) []string {
	var out []string
	for _, c := range companions {
		suffix := strings.TrimPrefix(c, weightsPath)
		if suffix == c {
			continue // not a sibling of the weights file
		}
		out = append(out, primaryName+suffix)
	}
	return out
}

// HFResolver resolves registry entries against the Hugging Face API.
type HFResolver struct {
	Client  *http.Client
	APIBase string
	// MaxFallbackDownload enables computing the SHA-256 by downloading non-LFS
	// files up to this size. 0 disables the fallback.
	MaxFallbackDownload int64

	// fileURL overrides the download URL used by the non-LFS fallback. It is
	// used by tests; production callers leave it nil.
	fileURL func(HFTarget) string
}

// NewHFResolver returns a resolver with the public API base and a conservative
// fallback download limit.
func NewHFResolver() *HFResolver {
	return &HFResolver{
		Client:              &http.Client{Timeout: 2 * time.Minute},
		APIBase:             DefaultHFAPIBase,
		MaxFallbackDownload: DefaultMaxFallbackHashSize,
	}
}

func (r *HFResolver) client() *http.Client {
	if r != nil && r.Client != nil {
		return r.Client
	}
	return http.DefaultClient
}

func (r *HFResolver) apiBase() string {
	if r != nil && r.APIBase != "" {
		return strings.TrimRight(r.APIBase, "/")
	}
	return DefaultHFAPIBase
}

func (r *HFResolver) maxFallback() int64 {
	if r != nil && r.MaxFallbackDownload > 0 {
		return r.MaxFallbackDownload
	}
	return 0
}

// downloadURL is the URL used by the non-LFS hashing fallback.
func (r *HFResolver) downloadURL(target HFTarget) string {
	if r != nil && r.fileURL != nil {
		return r.fileURL(target)
	}
	return target.URL
}

// Resolve returns the SHA-256 of a registry entry's weights file without
// downloading the weights when they are LFS-managed. Non-LFS files are hashed by
// downloading them only when smaller than MaxFallbackDownload.
func (r *HFResolver) Resolve(ctx context.Context, e Entry) (HFEntryHash, error) {
	target, err := ParseHFResolveURL(e.URL)
	if err != nil {
		return HFEntryHash{}, err
	}
	tree, err := r.Tree(ctx, target)
	if err != nil {
		return HFEntryHash{}, err
	}
	h, matchErr := MatchHFTree(target, tree)
	if matchErr != nil && !errors.Is(matchErr, ErrHFNoChecksum) {
		return h, matchErr
	}
	h.ID = e.ID
	h.ExtraFiles = LocalExtraFiles(PrimaryName(e), target.Path, h.Companions)
	if h.SHA256 != "" {
		return h, nil
	}
	max := r.maxFallback()
	if max <= 0 || h.Size <= 0 || h.Size > max {
		return h, fmt.Errorf("chronos: hf: %s is not LFS-managed and is %d bytes (fallback limit %d): %w",
			target.Path, h.Size, max, ErrHFNoChecksum)
	}
	sum, size, err := r.downloadSHA256(ctx, r.downloadURL(target), max)
	if err != nil {
		return h, fmt.Errorf("chronos: hf: hash %s: %w", target.Path, err)
	}
	h.SHA256, h.Size, h.Source = sum, size, "download"
	return h, nil
}

// Tree fetches the recursive file listing of a repository revision, following
// pagination links.
func (r *HFResolver) Tree(ctx context.Context, target HFTarget) ([]HFTreeFile, error) {
	base := r.apiBase()
	next := fmt.Sprintf("%s/api/models/%s/tree/%s?recursive=true", base, target.Repo, url.PathEscape(target.Revision))
	var all []HFTreeFile
	for page := 0; next != "" && page < maxTreePages; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		resp, err := r.client().Do(req)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		link := resp.Header.Get("Link")
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("chronos: hf: GET %s: http %d", next, resp.StatusCode)
		}
		var files []HFTreeFile
		if err := json.Unmarshal(body, &files); err != nil {
			return nil, fmt.Errorf("chronos: hf: decode tree of %s@%s: %w", target.Repo, target.Revision, err)
		}
		all = append(all, files...)
		next = nextLink(link, base)
	}
	return all, nil
}

// downloadSHA256 streams a file and returns its SHA-256 (metadata only; used for
// small non-LFS files).
func (r *HFResolver) downloadSHA256(ctx context.Context, rawURL string, limit int64) (string, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", 0, err
	}
	resp, err := r.client().Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("http %d", resp.StatusCode)
	}
	hasher := sha256.New()
	n, err := io.Copy(hasher, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", 0, err
	}
	if n > limit {
		return "", 0, fmt.Errorf("file is larger than %d bytes", limit)
	}
	return hex.EncodeToString(hasher.Sum(nil)), n, nil
}

// nextLink extracts the rel="next" URL from a Link header.
func nextLink(header, base string) string {
	for _, part := range strings.Split(header, ",") {
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start < 0 || end <= start {
			continue
		}
		link := strings.TrimSpace(part[start+1 : end])
		if strings.HasPrefix(link, "/") {
			return base + link
		}
		return link
	}
	return ""
}
