package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fixtureTree() []HFTreeFile {
	return []HFTreeFile{
		{Type: "directory", Path: "onnx", Size: 0},
		{Type: "file", Path: "README.md", Size: 100},
		{Type: "file", Path: "onnx/model.onnx", Size: 8078888, LFS: &HFLFS{
			OID: "c2abbfe0cc8e685b5e11964970f8ebe3d24072e904fd5545a30ac31ec1e110db", Size: 8078888, PointerSize: 132,
		}},
		{Type: "file", Path: "onnx/model.onnx.data", Size: 2048, LFS: &HFLFS{
			OID: strings.Repeat("d", 64), Size: 2048,
		}},
		{Type: "file", Path: "onnx/model_fp16.onnx", Size: 7524409, LFS: &HFLFS{
			OID: strings.Repeat("e", 64), Size: 7524409,
		}},
	}
}

func TestParseHFResolveURL(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    HFTarget
		wantErr string
	}{
		{
			name: "main revision",
			raw:  "https://huggingface.co/Heliosoph/realesrgan-onnx/resolve/main/realesr-general-x4v3.onnx",
			want: HFTarget{Repo: "Heliosoph/realesrgan-onnx", Revision: "main", Path: "realesr-general-x4v3.onnx"},
		},
		{
			name: "nested path",
			raw:  "https://huggingface.co/Xenova/swin2SR-lightweight-x2-64/resolve/main/onnx/model.onnx",
			want: HFTarget{Repo: "Xenova/swin2SR-lightweight-x2-64", Revision: "main", Path: "onnx/model.onnx"},
		},
		{
			name: "custom revision",
			raw:  "https://huggingface.co/owner/repo/resolve/dev/weights/model.onnx",
			want: HFTarget{Repo: "owner/repo", Revision: "dev", Path: "weights/model.onnx"},
		},
		{
			name: "blob url",
			raw:  "https://huggingface.co/owner/repo/blob/main/model.onnx",
			want: HFTarget{Repo: "owner/repo", Revision: "main", Path: "model.onnx"},
		},
		{
			name: "percent escaped path",
			raw:  "https://huggingface.co/owner/repo/resolve/main/dir/my%20model.onnx",
			want: HFTarget{Repo: "owner/repo", Revision: "main", Path: "dir/my model.onnx"},
		},
		{
			name:    "modelscope host",
			raw:     "https://www.modelscope.cn/models/Rokaa111/x/resolve/master/model.onnx",
			wantErr: "not a huggingface.co",
		},
		{
			name:    "missing resolve segment",
			raw:     "https://huggingface.co/owner/repo/main/model.onnx",
			wantErr: "want /<owner>/<repo>/resolve/",
		},
		{
			name:    "not a url",
			raw:     "model.onnx",
			wantErr: "scheme",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseHFResolveURL(tc.raw)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Repo != tc.want.Repo || got.Revision != tc.want.Revision || got.Path != tc.want.Path {
				t.Fatalf("target = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestMatchHFTree(t *testing.T) {
	target := HFTarget{Repo: "Xenova/swin2SR-lightweight-x2-64", Revision: "main", Path: "onnx/model.onnx"}
	h, err := MatchHFTree(target, fixtureTree())
	if err != nil {
		t.Fatal(err)
	}
	if h.SHA256 != "c2abbfe0cc8e685b5e11964970f8ebe3d24072e904fd5545a30ac31ec1e110db" {
		t.Fatalf("sha256 = %s", h.SHA256)
	}
	if h.Size != 8078888 || h.Source != "lfs" {
		t.Fatalf("hash = %+v", h)
	}
	if len(h.Companions) != 1 || h.Companions[0] != "onnx/model.onnx.data" {
		t.Fatalf("companions = %v", h.Companions)
	}
	if got := LocalExtraFiles(PrimaryONNX, target.Path, h.Companions); len(got) != 1 || got[0] != PrimaryONNX+DataSuffix {
		t.Fatalf("local extra files = %v", got)
	}
	if got := LocalExtraFiles(PrimaryONNX, target.Path, []string{"other/file.bin"}); len(got) != 0 {
		t.Fatalf("non-sibling companions = %v", got)
	}
}

func TestMatchHFTreeErrors(t *testing.T) {
	target := HFTarget{Repo: "owner/repo", Revision: "main", Path: "missing.onnx"}
	if _, err := MatchHFTree(target, fixtureTree()); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing file err = %v", err)
	}

	noLFS := []HFTreeFile{{Type: "file", Path: "small.onnx", Size: 1234}}
	h, err := MatchHFTree(HFTarget{Repo: "owner/repo", Revision: "main", Path: "small.onnx"}, noLFS)
	if !errors.Is(err, ErrHFNoChecksum) {
		t.Fatalf("non-LFS err = %v, want ErrHFNoChecksum", err)
	}
	if h.Size != 1234 || h.SHA256 != "" {
		t.Fatalf("non-LFS hash = %+v", h)
	}

	badOID := []HFTreeFile{{Type: "file", Path: "x.onnx", Size: 1, LFS: &HFLFS{OID: "abc"}}}
	if _, err := MatchHFTree(HFTarget{Repo: "owner/repo", Revision: "main", Path: "x.onnx"}, badOID); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("malformed oid err = %v", err)
	}
}

func hfEntry(id, rawURL string) Entry {
	return Entry{ID: id, Name: id, Scale: 2, URL: rawURL}
}

func TestHFResolverResolve(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		if err := json.NewEncoder(w).Encode(fixtureTree()); err != nil {
			t.Error(err)
		}
	}))
	defer srv.Close()

	res := &HFResolver{Client: srv.Client(), APIBase: srv.URL}
	e := hfEntry("swin2sr-lightweight-x2",
		"https://huggingface.co/Xenova/swin2SR-lightweight-x2-64/resolve/main/onnx/model.onnx")
	h, err := res.Resolve(t.Context(), e)
	if err != nil {
		t.Fatal(err)
	}
	if h.ID != e.ID || h.Repo != "Xenova/swin2SR-lightweight-x2-64" || h.Revision != "main" || h.Path != "onnx/model.onnx" {
		t.Fatalf("hash = %+v", h)
	}
	if h.SHA256 != "c2abbfe0cc8e685b5e11964970f8ebe3d24072e904fd5545a30ac31ec1e110db" {
		t.Fatalf("sha256 = %s", h.SHA256)
	}
	if len(h.ExtraFiles) != 1 || h.ExtraFiles[0] != PrimaryONNX+DataSuffix {
		t.Fatalf("extra files = %v", h.ExtraFiles)
	}
	if want := "/api/models/Xenova/swin2SR-lightweight-x2-64/tree/main"; gotPath != want {
		t.Fatalf("tree path = %q, want %q", gotPath, want)
	}
	if gotQuery != "recursive=true" {
		t.Fatalf("tree query = %q", gotQuery)
	}

	// The revision from the URL is used for the tree API.
	e = hfEntry("rev", "https://huggingface.co/Xenova/swin2SR-lightweight-x2-64/resolve/dev/onnx/model.onnx")
	if _, err := res.Resolve(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if want := "/api/models/Xenova/swin2SR-lightweight-x2-64/tree/dev"; gotPath != want {
		t.Fatalf("tree path = %q, want %q", gotPath, want)
	}
}

func TestHFResolverResolveErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"type":"file","path":"other.onnx","size":10}]`))
	}))
	defer srv.Close()
	res := &HFResolver{Client: srv.Client(), APIBase: srv.URL}
	if _, err := res.Resolve(t.Context(), hfEntry("x", "https://huggingface.co/o/r/resolve/main/model.onnx")); err == nil {
		t.Fatal("expected not-found error")
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer bad.Close()
	res = &HFResolver{Client: bad.Client(), APIBase: bad.URL}
	if _, err := res.Resolve(t.Context(), hfEntry("x", "https://huggingface.co/o/r/resolve/main/model.onnx")); err == nil || !strings.Contains(err.Error(), "http 404") {
		t.Fatalf("http error = %v", err)
	}

	res = &HFResolver{Client: bad.Client(), APIBase: bad.URL}
	if _, err := res.Resolve(t.Context(), hfEntry("x", "https://example.invalid/model.onnx")); err == nil {
		t.Fatal("expected parse error for a non-HF url")
	}
}

func TestHFResolverNonLFSFallback(t *testing.T) {
	payload := "small non-lfs model bytes"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/models/"):
			_, _ = w.Write([]byte(fmt.Sprintf(
				`[{"type":"file","path":"onnx/model.onnx","size":%d}]`, len(payload))))
		case r.URL.Path == "/weights/model.onnx":
			_, _ = w.Write([]byte(payload))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	res := &HFResolver{
		Client:              srv.Client(),
		APIBase:             srv.URL,
		MaxFallbackDownload: 1024,
		fileURL:             func(HFTarget) string { return srv.URL + "/weights/model.onnx" },
	}
	h, err := res.Resolve(t.Context(), hfEntry("small", "https://huggingface.co/o/r/resolve/main/onnx/model.onnx"))
	if err != nil {
		t.Fatal(err)
	}
	if h.Source != "download" {
		t.Fatalf("source = %q, want download", h.Source)
	}
	if want := sha256Hex(payload); h.SHA256 != want {
		t.Fatalf("sha256 = %s, want %s", h.SHA256, want)
	}
	if h.Size != int64(len(payload)) {
		t.Fatalf("size = %d", h.Size)
	}
}

func TestHFResolverNonLFSFallbackDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"type":"file","path":"onnx/model.onnx","size":1234}]`))
	}))
	defer srv.Close()

	res := &HFResolver{Client: srv.Client(), APIBase: srv.URL}
	if _, err := res.Resolve(t.Context(), hfEntry("small", "https://huggingface.co/o/r/resolve/main/onnx/model.onnx")); err == nil {
		t.Fatal("expected error when download fallback is disabled")
	}

	// Too large for the fallback limit.
	res = &HFResolver{
		Client:              srv.Client(),
		APIBase:             srv.URL,
		MaxFallbackDownload: 100,
		fileURL:             func(HFTarget) string { t.Fatal("must not download oversized files"); return "" },
	}
	if _, err := res.Resolve(t.Context(), hfEntry("small", "https://huggingface.co/o/r/resolve/main/onnx/model.onnx")); err == nil {
		t.Fatal("expected error for an oversized fallback download")
	}
}

func TestHFResolverPagination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			w.Header().Set("Link",
				fmt.Sprintf(`<%s/api/models/o/r/tree/main?recursive=true&cursor=page2>; rel="next"`, "http://"+r.Host))
			_, _ = w.Write([]byte(`[{"type":"file","path":"first.onnx","size":1}]`))
			return
		}
		_, _ = w.Write([]byte(`[{"type":"file","path":"onnx/model.onnx","size":10,` +
			`"lfs":{"oid":"` + strings.Repeat("f", 64) + `","size":10}}]`))
	}))
	defer srv.Close()

	res := &HFResolver{Client: srv.Client(), APIBase: srv.URL}
	h, err := res.Resolve(t.Context(), hfEntry("paged", "https://huggingface.co/o/r/resolve/main/onnx/model.onnx"))
	if err != nil {
		t.Fatal(err)
	}
	if h.SHA256 != strings.Repeat("f", 64) {
		t.Fatalf("sha256 = %s", h.SHA256)
	}
}
