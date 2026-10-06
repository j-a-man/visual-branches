// Package web serves vb's local web view.
//
// The server binds to 127.0.0.1 only and rejects requests whose Host header
// is not local, which blocks DNS rebinding. The page is a small TypeScript
// app embedded in the binary; it renders the same layout as `vb export svg`
// and updates live over server-sent events when branches change.
package web

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/j-a-man/visual-branches/internal/engine"
	"github.com/j-a-man/visual-branches/internal/model"
	"github.com/j-a-man/visual-branches/internal/render"
	"github.com/j-a-man/visual-branches/internal/theme"
)

//go:embed dist
var dist embed.FS

// Options configure the server.
type Options struct {
	Session *engine.Session
	Port    int
	// Ready is called with the URL once the server is listening.
	Ready func(url string)
	// Poll is how often the repository is checked for changes.
	Poll time.Duration
}

// Server is the running web view.
type Server struct {
	o    Options
	s    *engine.Session
	mu   sync.Mutex
	maps map[string]*model.Map // by source
	fp   string
	subs map[chan string]struct{}
	host string
}

// Serve runs the web view until ctx is cancelled.
func Serve(ctx context.Context, o Options) error {
	if o.Poll <= 0 {
		o.Poll = 2 * time.Second
	}
	srv := &Server{o: o, s: o.Session, maps: map[string]*model.Map{}, subs: map[chan string]struct{}{}}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", o.Port))
	if err != nil {
		// The configured port is busy; take any free port.
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
	}
	port := ln.Addr().(*net.TCPAddr).Port
	srv.host = strconv.Itoa(port)
	httpSrv := &http.Server{Handler: srv.routes(), ReadHeaderTimeout: 10 * time.Second}
	go srv.watch(ctx)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdown)
	}()
	if o.Ready != nil {
		o.Ready(fmt.Sprintf("http://127.0.0.1:%d/", port))
	}
	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (srv *Server) routes() http.Handler {
	mux := http.NewServeMux()
	assets, _ := fs.Sub(dist, "dist")
	// VB_WEB_DIR serves assets from disk, for working on the web view with
	// `npm run watch` without rebuilding the binary.
	if dir := os.Getenv("VB_WEB_DIR"); dir != "" {
		assets = os.DirFS(dir)
	}
	files := http.FileServer(http.FS(assets))
	mux.Handle("GET /", noCache(files))
	mux.HandleFunc("GET /api/map", srv.handleMap)
	mux.HandleFunc("GET /api/branch", srv.handleBranch)
	mux.HandleFunc("GET /api/events", srv.handleEvents)
	mux.HandleFunc("GET /api/export/{format}", srv.handleExport)
	return srv.localOnly(mux)
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

// localOnly rejects requests whose Host is not this loopback server, which
// protects against DNS rebinding from malicious web pages.
func (srv *Server) localOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, port, err := net.SplitHostPort(r.Host)
		if err != nil || port != srv.host || (host != "127.0.0.1" && host != "localhost" && host != "[::1]" && host != "::1") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'")
		h.ServeHTTP(w, r)
	})
}

func source(r *http.Request) string {
	if r.URL.Query().Get("source") == engine.SourceAll {
		return engine.SourceAll
	}
	return engine.SourceLocal
}

// current returns the latest map for a source, building it if needed.
func (srv *Server) current(ctx context.Context, src string) (*model.Map, error) {
	srv.mu.Lock()
	m := srv.maps[src]
	srv.mu.Unlock()
	if m != nil {
		return m, nil
	}
	return srv.rebuild(ctx, src)
}

func (srv *Server) rebuild(ctx context.Context, src string) (*model.Map, error) {
	s := *srv.s
	s.Opts.Source = src
	m, err := s.Build(ctx)
	if err != nil {
		return nil, err
	}
	srv.mu.Lock()
	srv.maps[src] = m
	srv.mu.Unlock()
	return m, nil
}

type mapResponse struct {
	Map    *model.Map    `json:"map"`
	Layout render.Layout `json:"layout"`
	Themes struct {
		Light theme.Theme `json:"light"`
		Dark  theme.Theme `json:"dark"`
		Mode  string      `json:"mode"`
	} `json:"themes"`
	Remote string `json:"remoteUrl,omitempty"`
}

func (srv *Server) handleMap(w http.ResponseWriter, r *http.Request) {
	m, err := srv.current(r.Context(), source(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cfg := srv.s.Config
	flt := render.Filter{ShowMerged: r.URL.Query().Get("merged") != "0", Hide: cfg.Display.Hide}
	var resp mapResponse
	resp.Map = m
	resp.Layout = render.ComputeLayout(m, render.Rows(m, flt), time.Now())
	resp.Remote = m.Repo.Remote.WebURL()
	resp.Themes.Mode = "auto"
	light, dark := cfg.Web.LightTheme, cfg.Web.DarkTheme
	if cfg.Web.Theme != "auto" {
		light, dark = cfg.Web.Theme, cfg.Web.Theme
		resp.Themes.Mode = "fixed"
	}
	lt, _ := theme.Resolve(light, cfg.Themes)
	dt, _ := theme.Resolve(dark, cfg.Themes)
	resp.Themes.Light, resp.Themes.Dark = lt.Web(), dt.Web()
	writeJSON(w, resp)
}

func (srv *Server) handleBranch(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	m, err := srv.current(r.Context(), source(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s := *srv.s
	s.Opts.Source = source(r)
	d, err := s.Detail(r.Context(), m, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, d)
}

func (srv *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	m, err := srv.current(r.Context(), source(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cfg := srv.s.Config
	th, _ := theme.Resolve(cfg.Web.DarkTheme, cfg.Themes)
	if r.URL.Query().Get("theme") == "light" {
		th, _ = theme.Resolve(cfg.Web.LightTheme, cfg.Themes)
	}
	o := render.ExportOptions{Theme: th, Now: time.Now(), Filter: render.Filter{ShowMerged: r.URL.Query().Get("merged") != "0", Hide: cfg.Display.Hide}}
	switch r.PathValue("format") {
	case "svg":
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Content-Disposition", `attachment; filename="`+m.Repo.Name+`-branches.svg"`)
		fmt.Fprint(w, render.SVG(m, o))
	case "mermaid":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, render.Mermaid(m, o))
	default:
		http.NotFound(w, r)
	}
}

// handleEvents streams "changed" events whenever the map changes.
func (srv *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch := make(chan string, 4)
	srv.mu.Lock()
	srv.subs[ch] = struct{}{}
	srv.mu.Unlock()
	defer func() {
		srv.mu.Lock()
		delete(srv.subs, ch)
		srv.mu.Unlock()
	}()
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	keepAlive := time.NewTicker(25 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case state := <-ch:
			fmt.Fprintf(w, "event: changed\ndata: %s\n\n", state)
			flusher.Flush()
		case <-keepAlive.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (srv *Server) broadcast(state string) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	for ch := range srv.subs {
		select {
		case ch <- state:
		default:
		}
	}
}

// watch rebuilds when refs, HEAD, or any worktree index changes, and at
// least once per GitHub cache TTL so PR and CI data stay fresh.
func (srv *Server) watch(ctx context.Context) {
	ttl := srv.s.Config.GitHub.CacheTTL.Duration
	if ttl < 15*time.Second {
		ttl = 15 * time.Second
	}
	lastFull := time.Now()
	tick := time.NewTicker(srv.o.Poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		fp := srv.fingerprint(ctx)
		srv.mu.Lock()
		changed := fp != srv.fp
		srv.fp = fp
		sources := make([]string, 0, len(srv.maps))
		for src := range srv.maps {
			sources = append(sources, src)
		}
		srv.mu.Unlock()
		if !changed && time.Since(lastFull) < ttl {
			continue
		}
		lastFull = time.Now()
		for _, src := range sources {
			srv.mu.Lock()
			old := srv.maps[src]
			srv.mu.Unlock()
			m, err := srv.rebuild(ctx, src)
			if err != nil {
				continue
			}
			if old == nil || old.State != m.State {
				srv.broadcast(m.State)
			}
		}
	}
}

// fingerprint is a cheap hash of everything that can change the map
// locally: ref tips, HEAD, and the modification times of index files.
func (srv *Server) fingerprint(ctx context.Context) string {
	h := sha256.New()
	out, _ := srv.s.Repo.Run(ctx, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/remotes")
	h.Write([]byte(out))
	common := srv.s.Repo.CommonDir
	stamp := func(p string) {
		if st, err := os.Stat(p); err == nil {
			fmt.Fprintf(h, "%s %d %d\n", p, st.ModTime().UnixNano(), st.Size())
		}
	}
	stamp(filepath.Join(common, "HEAD"))
	stamp(filepath.Join(common, "index"))
	if entries, err := os.ReadDir(filepath.Join(common, "worktrees")); err == nil {
		for _, e := range entries {
			stamp(filepath.Join(common, "worktrees", e.Name(), "HEAD"))
			stamp(filepath.Join(common, "worktrees", e.Name(), "index"))
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
