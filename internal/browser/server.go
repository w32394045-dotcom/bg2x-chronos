// Package browser runs the Chronos UI in the user's default browser. It serves
// the embedded (or on-disk) frontend over 127.0.0.1 and exposes the same JSON
// API as the Wails bindings, bridging events through Server-Sent Events. This is
// what keeps Chronos usable on machines without WebView2/WebKit.
package browser

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/w32394045-dotcom/bg2x-chronos/internal/uiapp"
)

// Options configures one browser-mode server instance.
type Options struct {
	Addr   string // default 127.0.0.1
	Port   int    // 0 => random free port
	Open   bool   // open the system browser
	JSON   bool   // print a machine-readable startup line on stdout
	Assets fs.FS  // required, must contain index.html
	Logger *log.Logger
}

type sseEvent struct {
	Name string `json:"name"`
	Data any    `json:"data"`
}

// Server is the loopback HTTP server.
type Server struct {
	core   *uiapp.Core
	assets fs.FS
	token  string
	url    string

	mu      sync.Mutex
	clients map[chan sseEvent]struct{}
	closed  chan struct{}
	closeIn sync.Once

	logger     *log.Logger
	httpServer *http.Server
}

// Run starts the server, prints its URL, optionally opens the browser and blocks
// until ctx is cancelled or the server fails.
func Run(ctx context.Context, core *uiapp.Core, opts Options) error {
	srv, listener, err := Start(core, opts)
	if err != nil {
		return err
	}
	if opts.JSON {
		payload, _ := json.Marshal(map[string]any{
			"mode": uiapp.ModeBrowser,
			"url":  srv.url,
			"pid":  os.Getpid(),
			"addr": listener.Addr().String(),
		})
		fmt.Fprintln(os.Stdout, string(payload))
	} else {
		fmt.Fprintf(os.Stderr, "Chronos 濞村繗顫嶉崳銊δ佸蹇撳嚒閸氼垰濮? %s\n閹?Ctrl+C 闁偓閸戞亽鈧繐n", srv.url)
	}
	if opts.Open {
		if err := openBrowser(srv.url); err != nil {
			fmt.Fprintf(os.Stderr, "閺冪姵纭堕懛顏勫З閹垫挸绱戝ù蹇氼潔閸? %v\n鐠囬攱澧滈崝銊問闂?%s\n", err, srv.url)
		}
	}

	serveErr := make(chan error, 1)
	go func() {
		httpServer := &http.Server{Handler: srv}
		srv.httpServer = httpServer
		err := httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case <-ctx.Done():
		srv.close()
		select {
		case <-serveErr:
		case <-time.After(3 * time.Second):
		}
		return nil
	case err := <-serveErr:
		srv.close()
		if err != nil {
			return fmt.Errorf("chronos: browser server: %w", err)
		}
		return nil
	}
}

// Start binds the listener and initialises the core in browser mode. It is
// exported so tests and the CLI smoke test can control the lifecycle.
func Start(core *uiapp.Core, opts Options) (*Server, net.Listener, error) {
	if core == nil {
		return nil, nil, fmt.Errorf("chronos: browser mode needs an app core")
	}
	if opts.Assets == nil {
		return nil, nil, fmt.Errorf("chronos: browser mode needs UI assets")
	}
	if _, err := fs.Stat(opts.Assets, "index.html"); err != nil {
		return nil, nil, fmt.Errorf("chronos: UI assets are missing index.html (build the frontend first): %w", err)
	}
	addr := strings.TrimSpace(opts.Addr)
	if addr == "" {
		addr = "127.0.0.1"
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.New(os.Stderr, "[browser] ", log.LstdFlags)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(addr, strconv.Itoa(opts.Port)))
	if err != nil {
		return nil, nil, fmt.Errorf("chronos: bind %s:%d: %w", addr, opts.Port, err)
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		listener.Close()
		return nil, nil, fmt.Errorf("chronos: generate session token: %w", err)
	}
	s := &Server{
		core:    core,
		assets:  opts.Assets,
		token:   hex.EncodeToString(tokenBytes),
		clients: make(map[chan sseEvent]struct{}),
		closed:  make(chan struct{}),
		logger:  logger,
	}
	s.url = fmt.Sprintf("http://%s/", listener.Addr().String())

	// Same app core either shell uses; only the sink and mode change.
	core.SetMode(uiapp.ModeBrowser)
	core.SetEmitter(s.Broadcast)
	core.Startup()
	return s, listener, nil
}

// URL is the loopback address the UI is served from.
func (s *Server) URL() string { return s.url }

// Broadcast implements uiapp.Emitter.
func (s *Server) Broadcast(name string, payload any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.clients {
		select {
		case ch <- sseEvent{Name: name, Data: payload}:
		default:
			// Slow/stalled client: drop instead of blocking the scheduler.
		}
	}
}

func (s *Server) close() {
	s.closeIn.Do(func() {
		close(s.closed)
		if s.httpServer != nil {
			_ = s.httpServer.Close()
		}
		s.core.Shutdown()
	})
}

// ---- HTTP plumbing ----------------------------------------------------------

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/healthz":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	case r.URL.Path == "/events":
		s.handleEvents(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/"):
		s.handleAPI(w, r)
	case r.URL.Path == "/" || r.URL.Path == "/index.html":
		s.handleIndex(w, r)
	default:
		s.handleStatic(w, r)
	}
}

func (s *Server) securityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
}

// sameOrigin rejects cross-site requests: only the exact loopback origin (or a
// request with no Origin header, e.g. curl) is accepted.
func (s *Server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	return origin == "http://"+strings.TrimSuffix(strings.TrimPrefix(s.url, "http://"), "/")
}

// sessionOK enforces the per-process session cookie set by the index handler.
func (s *Server) sessionOK(r *http.Request) bool {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	return subtleEqual(cookie.Value, s.token)
}

const cookieName = "chronos_session"

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.securityHeaders(w)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    s.token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	raw, err := fs.ReadFile(s.assets, "index.html")
	if err != nil {
		http.Error(w, "index.html missing", http.StatusInternalServerError)
		return
	}
	injected := strings.Replace(string(raw), "</head>",
		"<script>window.__CHRONOS_HTTP__=true;</script></head>", 1)
	if !strings.Contains(injected, "__CHRONOS_HTTP__") {
		injected = "<script>window.__CHRONOS_HTTP__=true;</script>" + injected
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, injected)
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	clean := path.Clean("/" + r.URL.Path)
	if clean == "/" {
		s.handleIndex(w, r)
		return
	}
	name := strings.TrimPrefix(clean, "/")
	f, err := s.assets.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || stat.IsDir() {
		http.NotFound(w, r)
		return
	}
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "read asset failed", http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, name, stat.ModTime(), bytes.NewReader(data))
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	s.securityHeaders(w)
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "forbidden: cross-origin request")
		return
	}
	if !s.sessionOK(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized: open the app URL in the browser first")
		return
	}
	method := strings.TrimPrefix(r.URL.Path, "/api/")
	if method == "" {
		writeError(w, http.StatusNotFound, "missing method")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var req struct {
		Args []json.RawMessage `json:"args"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}
	result, err := s.dispatch(method, req.Args)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	s.securityHeaders(w)
	if !s.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "forbidden: cross-origin request")
		return
	}
	if !s.sessionOK(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan sseEvent, 256)
	s.mu.Lock()
	s.clients[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, ch)
		s.mu.Unlock()
	}()

	_, _ = io.WriteString(w, ": connected\n\n")
	flusher.Flush()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.closed:
			return
		case ev := <-ch:
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// ---- dispatch ---------------------------------------------------------------

func (s *Server) dispatch(method string, args []json.RawMessage) (any, error) {
	switch method {
	case "GetAppInfo":
		return s.core.GetAppInfo(), nil
	case "ListModels", "RefreshModels":
		return s.core.ListModels(), nil
	case "UpscaleBatch":
		var req uiapp.BatchRequest
		if err := decodeArg(args, 0, &req); err != nil {
			return nil, err
		}
		return s.core.UpscaleBatch(req)
	case "CancelJob":
		var id string
		if err := decodeArg(args, 0, &id); err != nil {
			return nil, err
		}
		return s.core.CancelJob(id), nil
	case "SetConcurrency":
		var n int
		if err := decodeArg(args, 0, &n); err != nil {
			return nil, err
		}
		return nil, s.core.SetConcurrency(n)
	case "GetConcurrency":
		return s.core.GetConcurrency(), nil
	case "SetInhibit":
		var req uiapp.InhibitRequest
		if err := decodeArg(args, 0, &req); err != nil {
			return nil, err
		}
		return s.core.SetInhibit(req)
	case "GetInhibitStatus":
		return s.core.GetInhibitStatus(), nil
	case "PickImages":
		return s.core.PickImages(), nil
	case "PickOutputDir":
		return s.core.PickOutputDir(), nil
	case "PickModelFile":
		// Browser mode has no native dialog: the UI falls back to ListDirectory.
		return s.core.PickModelFile(), nil
	case "PullModel":
		var req uiapp.PullModelRequest
		if err := decodeArg(args, 0, &req); err != nil {
			return nil, err
		}
		return s.core.PullModel(req)
	case "CancelPull":
		var id string
		if err := decodeArg(args, 0, &id); err != nil {
			return nil, err
		}
		return s.core.CancelPull(id), nil
	case "RemoveModel":
		var id string
		if err := decodeArg(args, 0, &id); err != nil {
			return nil, err
		}
		return nil, s.core.RemoveModel(id)
	case "ImportModel":
		var req uiapp.ImportModelRequest
		if err := decodeArg(args, 0, &req); err != nil {
			return nil, err
		}
		return s.core.ImportModel(req)
	case "SelfTestModel":
		var id string
		if err := decodeArg(args, 0, &id); err != nil {
			return nil, err
		}
		return s.core.SelfTestModel(id, true), nil
	case "GetModelDiagnostics":
		var id string
		if err := decodeArg(args, 0, &id); err != nil {
			return nil, err
		}
		return s.core.GetModelDiagnostics(id), nil
	case "GetAccelStatus":
		return s.core.GetAccelStatus(), nil
	case "SetAccelMode":
		var mode string
		if err := decodeArg(args, 0, &mode); err != nil {
			return nil, err
		}
		return s.core.SetAccelMode(mode)
	case "InstallAccelRuntime":
		return s.core.InstallAccelRuntime()
	case "Reveal":
		var p string
		if err := decodeArg(args, 0, &p); err != nil {
			return nil, err
		}
		return nil, s.core.Reveal(p)
	case "OpenDataDir":
		return nil, s.core.OpenDataDir()
	case "ListDirectory":
		var p string
		if len(args) > 0 {
			if err := json.Unmarshal(args[0], &p); err != nil {
				return nil, fmt.Errorf("chronos: invalid argument: %w", err)
			}
		}
		return s.core.ListDirectory(p)
	}
	return nil, fmt.Errorf("chronos: unknown method %q", method)
}

func decodeArg(args []json.RawMessage, index int, out any) error {
	if index >= len(args) {
		return fmt.Errorf("chronos: missing argument %d", index)
	}
	if err := json.Unmarshal(args[index], out); err != nil {
		return fmt.Errorf("chronos: invalid argument %d: %w", index, err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "linux":
		if _, err := exec.LookPath("xdg-open"); err == nil {
			return exec.Command("xdg-open", url).Start()
		}
		for _, cmd := range []string{"gio", "sensible-browser", "x-www-browser"} {
			if _, err := exec.LookPath(cmd); err == nil {
				return exec.Command(cmd, url).Start()
			}
		}
		return fmt.Errorf("no browser launcher found (tried xdg-open/gio/sensible-browser)")
	case "darwin":
		return exec.Command("open", url).Start()
	}
	return fmt.Errorf("opening a browser is not supported on %s", runtime.GOOS)
}
