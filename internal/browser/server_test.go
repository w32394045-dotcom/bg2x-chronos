package browser

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/w32394045-dotcom/bg2x-chronos/internal/uiapp"
)

func isolateUserDirs(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	for _, dir := range []string{
		filepath.Join(home, "AppData", "Roaming"),
		filepath.Join(home, "AppData", "Local"),
		filepath.Join(home, ".config"),
		filepath.Join(home, ".cache"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func assetsFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<html><head><title>x</title></head><body>chronos</body></html>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('chronos')")},
	}
}

func startTestServer(t *testing.T) (*Server, *http.Client, string) {
	t.Helper()
	isolateUserDirs(t)
	core := uiapp.New(uiapp.BuildInfo{Version: "test"})
	srv, listener, err := Start(core, Options{Assets: assetsFS(), Addr: "127.0.0.1", Port: 0, Open: false})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		hs := &http.Server{Handler: srv}
		srv.httpServer = hs
		_ = hs.Serve(listener)
	}()
	t.Cleanup(func() { srv.close() })

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	return srv, client, srv.URL()
}

func apiCall(t *testing.T, client *http.Client, base, method string, args ...any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"args": args})
	resp, err := client.Post(base+"api/"+method, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s: status %d: %s", method, resp.StatusCode, raw)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("%s: decode: %v", method, err)
	}
	return out
}

func TestIndexInjectsBrowserFlagAndCookie(t *testing.T) {
	_, client, base := startTestServer(t)
	resp, err := client.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("index status = %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "window.__CHRONOS_HTTP__=true") {
		t.Fatalf("index was not injected with the browser-mode flag: %s", body)
	}
	u, _ := resp.Request.URL.Parse("/")
	if cookies := client.Jar.Cookies(u); len(cookies) == 0 {
		t.Fatal("session cookie was not set by the index handler")
	}
	// Static asset is served from the same FS.
	asset, err := client.Get(base + "assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	defer asset.Body.Close()
	if asset.StatusCode != http.StatusOK {
		t.Fatalf("asset status = %d", asset.StatusCode)
	}
}

func TestAPIRequiresSessionAndOrigin(t *testing.T) {
	_, client, base := startTestServer(t)

	// No cookie yet: rejected.
	resp, err := http.Post(base+"api/GetAppInfo", "application/json", strings.NewReader(`{"args":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without cookie, got %d", resp.StatusCode)
	}

	// Establish the cookie, then a foreign Origin must be rejected.
	if _, err := client.Get(base); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, base+"api/GetAppInfo", strings.NewReader(`{"args":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for foreign origin, got %d", resp.StatusCode)
	}
}

func TestAPIDispatch(t *testing.T) {
	_, client, base := startTestServer(t)
	if _, err := client.Get(base); err != nil {
		t.Fatal(err)
	}

	info := apiCall(t, client, base, "GetAppInfo")
	result, ok := info["result"].(map[string]any)
	if !ok {
		t.Fatalf("GetAppInfo result = %+v", info)
	}
	if result["mode"] != "browser" || result["browseSupported"] != true {
		t.Fatalf("app info = %+v", result)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	listing := apiCall(t, client, base, "ListDirectory", dir)
	res, _ := json.Marshal(listing)
	if !strings.Contains(string(res), "\"a.png\"") || !strings.Contains(string(res), "\"isImage\":true") {
		t.Fatalf("ListDirectory result = %s", res)
	}

	// Unknown methods become a JSON error, not a broken connection.
	if err := func() error {
		body, _ := json.Marshal(map[string]any{"args": []any{}})
		resp, err := client.Post(base+"api/NoSuchMethod", "application/json", bytes.NewReader(body))
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return err
		}
		if !strings.Contains(out["error"].(string), "unknown method") {
			return io.ErrUnexpectedEOF
		}
		return nil
	}(); err != nil {
		t.Fatalf("unknown method handling: %v", err)
	}

	// Bad JSON is a 400.
	resp, err := client.Post(base+"api/GetAppInfo", "application/json", strings.NewReader("{broken"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad JSON status = %d, want 400", resp.StatusCode)
	}
}

func TestEventsStream(t *testing.T) {
	_, client, base := startTestServer(t)
	if _, err := client.Get(base); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, base+"events", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("events content type = %q", ct)
	}
	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, ":") {
		t.Fatalf("expected SSE comment preamble, got %q", line)
	}
}
