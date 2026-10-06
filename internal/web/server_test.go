package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/j-a-man/visual-branches/internal/config"
	"github.com/j-a-man/visual-branches/internal/engine"
	"github.com/j-a-man/visual-branches/internal/model"
	"github.com/j-a-man/visual-branches/internal/testrepo"
)

func server(t *testing.T) (*Server, *testrepo.Repo) {
	t.Helper()
	testrepo.Isolate(t)
	r := testrepo.New(t)
	r.Branch("feat/a", "main")
	r.Commits("feat/a", 2)
	r.Branch("feat/b", "feat/a")
	r.Commits("feat/b", 1)
	s, err := engine.Open(context.Background(), engine.Options{Dir: r.Dir, Config: config.Defaults(), GitHub: "never"})
	if err != nil {
		t.Fatal(err)
	}
	return &Server{o: Options{Session: s}, s: s, maps: map[string]*model.Map{}, subs: map[chan string]struct{}{}, host: "7878"}, r
}

func get(t *testing.T, h http.Handler, host, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRejectsForeignHosts(t *testing.T) {
	srv, _ := server(t)
	h := srv.routes()
	for _, host := range []string{"evil.example.com:7878", "127.0.0.1:9999", "attacker.localhost:7878"} {
		if rec := get(t, h, host, "/api/map"); rec.Code != http.StatusForbidden {
			t.Errorf("host %s: status %d", host, rec.Code)
		}
	}
	for _, host := range []string{"127.0.0.1:7878", "localhost:7878"} {
		if rec := get(t, h, host, "/api/map"); rec.Code != http.StatusOK {
			t.Errorf("host %s: status %d: %s", host, rec.Code, rec.Body)
		}
	}
}

func TestMapBranchAndExport(t *testing.T) {
	srv, _ := server(t)
	h := srv.routes()
	rec := get(t, h, "127.0.0.1:7878", "/api/map")
	var resp mapResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Layout.Nodes) != 3 || len(resp.Layout.Edges) != 2 || resp.Themes.Dark.Bg == "" || resp.Themes.Light.Bg == "" {
		t.Errorf("map response: %+v", resp.Layout)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("missing CSP: %q", csp)
	}

	rec = get(t, h, "127.0.0.1:7878", "/api/branch?name=feat/b")
	var d struct {
		Branch  model.Branch `json:"branch"`
		Commits []any        `json:"commits"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil || d.Branch.Parent != "feat/a" || len(d.Commits) != 1 {
		t.Errorf("branch detail: %v %+v", err, d)
	}
	if rec := get(t, h, "127.0.0.1:7878", "/api/branch?name=nope"); rec.Code != http.StatusNotFound {
		t.Errorf("missing branch status %d", rec.Code)
	}

	rec = get(t, h, "127.0.0.1:7878", "/api/export/svg")
	body, _ := io.ReadAll(rec.Body)
	if rec.Header().Get("Content-Type") != "image/svg+xml" || !strings.HasPrefix(string(body), "<svg") {
		t.Errorf("svg export: %s", rec.Header())
	}
	rec = get(t, h, "127.0.0.1:7878", "/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/app.js") {
		t.Errorf("index: %d %s", rec.Code, rec.Body)
	}
}

func TestFingerprintChangesWithRefs(t *testing.T) {
	srv, r := server(t)
	ctx := context.Background()
	a := srv.fingerprint(ctx)
	if b := srv.fingerprint(ctx); a != b {
		t.Fatal("fingerprint not stable")
	}
	r.Commits("feat/b", 1)
	if c := srv.fingerprint(ctx); c == a {
		t.Error("fingerprint did not change after a commit")
	}
}
