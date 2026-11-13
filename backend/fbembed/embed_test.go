package fbembed

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newEmbed boots an instance against a scratch scope holding one file and
// registers the closer. Instances are process-global, so tests run serially.
func newEmbed(t *testing.T, allowWrite bool) (http.Handler, string) {
	t.Helper()
	scope := t.TempDir()
	if err := os.WriteFile(filepath.Join(scope, "hello.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, closer, err := New(Options{Scope: scope, AllowWrite: allowWrite})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := closer(); err != nil {
			t.Errorf("closer: %v", err)
		}
	})
	return h, scope
}

func do(t *testing.T, h http.Handler, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// appConfig extracts the JSON the index page hands the SPA.
func appConfig(t *testing.T, body string) map[string]any {
	t.Helper()
	const open = `id="app-config"`
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatalf("no app-config block in page:\n%s", body)
	}
	rest := body[i+len(open):]
	rest = rest[strings.Index(rest, ">")+1:]
	rest = rest[:strings.Index(rest, "</script>")]
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(rest)), &m); err != nil {
		t.Fatalf("app-config json: %v\n%s", err, rest)
	}
	return m
}

func TestIndexRendersAgainstConfiguredBase(t *testing.T) {
	h, _ := newEmbed(t, false)
	rr := do(t, h, http.MethodGet, "/", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET / = %d: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type %q", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"/public/static/`) {
		t.Fatalf("page does not reference /public/static:\n%s", body[:400])
	}
	cfg := appConfig(t, body)
	if cfg["baseURL"] != "/" {
		t.Errorf("baseURL = %v, want /", cfg["baseURL"])
	}
	if cfg["noAuth"] != true {
		t.Errorf("noAuth = %v, want true", cfg["noAuth"])
	}
	if cfg["name"] != "Files" {
		t.Errorf("name = %v, want Files", cfg["name"])
	}
}

func TestIndexHonorsForwardedPrefix(t *testing.T) {
	h, _ := newEmbed(t, false)
	rr := do(t, h, http.MethodGet, "/", map[string]string{"X-Forwarded-Prefix": "/matrix/app/files"})
	if rr.Code != http.StatusOK {
		t.Fatalf("GET / = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"/matrix/app/files/public/static/`) {
		t.Fatalf("page does not emit the forwarded prefix in static URLs")
	}
	if strings.Contains(body, `href="/public/static/`) {
		t.Fatalf("page still emits un-prefixed static URLs")
	}
	cfg := appConfig(t, body)
	if cfg["baseURL"] != "/matrix/app/files/" {
		t.Errorf("baseURL = %v, want /matrix/app/files/", cfg["baseURL"])
	}
}

func TestStaticAssetsServeFromEmbeddedZip(t *testing.T) {
	h, _ := newEmbed(t, false)
	rr := do(t, h, http.MethodGet, "/", nil)
	body := rr.Body.String()
	// Pick the first module script the page loads and fetch it.
	i := strings.Index(body, `src="/public/static/assets/`)
	if i < 0 {
		t.Fatalf("no module script in page")
	}
	src := body[i+len(`src="`):]
	src = src[:strings.Index(src, `"`)]
	rr = do(t, h, http.MethodGet, src, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", src, rr.Code)
	}
	if rr.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("js served without gzip encoding: %v", rr.Header())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("js content-type %q", ct)
	}
	rr = do(t, h, http.MethodGet, "/public/static/css/loadingSpinners.css", nil)
	if rr.Code != http.StatusOK || rr.Body.Len() == 0 {
		t.Errorf("css asset = %d, %d bytes", rr.Code, rr.Body.Len())
	}
}

func TestHealthAndListing(t *testing.T) {
	h, _ := newEmbed(t, false)
	rr := do(t, h, http.MethodGet, "/health", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /health = %d", rr.Code)
	}
	// The listing is served from the index or straight from disk while the
	// first scan runs; either way the scope file must be visible promptly.
	deadline := time.Now().Add(10 * time.Second)
	for {
		rr = do(t, h, http.MethodGet, "/api/resources?path=/&source="+SourceName, nil)
		if rr.Code == http.StatusOK && strings.Contains(rr.Body.String(), `"hello.txt"`) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET /api/resources = %d: %s", rr.Code, rr.Body.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestLoneUserIsHardened(t *testing.T) {
	h, _ := newEmbed(t, false)
	rr := do(t, h, http.MethodGet, "/api/users?username=self", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/users?username=self = %d: %s", rr.Code, rr.Body.String())
	}
	var u struct {
		Permissions struct {
			Admin bool `json:"admin"`
			Share bool `json:"share"`
			Api   bool `json:"api"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &u); err != nil {
		t.Fatalf("user json: %v: %s", err, rr.Body.String())
	}
	if !u.Permissions.Admin || u.Permissions.Share || u.Permissions.Api {
		t.Errorf("perms = %+v, want admin only", u.Permissions)
	}
}

func TestReadOnlyRefusesWrites(t *testing.T) {
	h, scope := newEmbed(t, false)
	rr := do(t, h, http.MethodPost, "/api/resources?path=/new.txt&source="+SourceName, nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("POST on read-only = %d, want 403: %s", rr.Code, rr.Body.String())
	}
	rr = do(t, h, http.MethodDelete, "/api/resources?path=/hello.txt&source="+SourceName, nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("DELETE on read-only = %d, want 403: %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(scope, "hello.txt")); err != nil {
		t.Fatalf("read-only delete removed the file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(scope, "new.txt")); err == nil {
		t.Fatalf("read-only create wrote a file")
	}
}

func TestAllowWriteCreatesAndDeletes(t *testing.T) {
	h, scope := newEmbed(t, true)
	req := httptest.NewRequest(http.MethodPost, "/api/resources?path=/new.txt&source="+SourceName, strings.NewReader("data"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code/100 != 2 {
		t.Fatalf("POST with AllowWrite = %d: %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(scope, "new.txt")); err != nil {
		t.Fatalf("create did not write the file: %v", err)
	}
	rr = do(t, h, http.MethodDelete, "/api/resources?path=/hello.txt&source="+SourceName, nil)
	if rr.Code/100 != 2 {
		t.Fatalf("DELETE with AllowWrite = %d: %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(scope, "hello.txt")); !os.IsNotExist(err) {
		t.Fatalf("delete left the file: %v", err)
	}
}

func TestSingleInstanceAndRestart(t *testing.T) {
	scope := t.TempDir()
	h, closer, err := New(Options{Scope: scope})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, _, err := New(Options{Scope: scope}); err != ErrActive {
		t.Fatalf("second New = %v, want ErrActive", err)
	}
	if rr := do(t, h, http.MethodGet, "/health", nil); rr.Code != http.StatusOK {
		t.Fatalf("health before close = %d", rr.Code)
	}
	if err := closer(); err != nil {
		t.Fatalf("closer: %v", err)
	}
	if err := closer(); err != nil {
		t.Fatalf("second closer: %v", err)
	}
	h2, closer2, err := New(Options{Scope: scope, AllowWrite: true})
	if err != nil {
		t.Fatalf("New after close: %v", err)
	}
	defer closer2()
	if rr := do(t, h2, http.MethodGet, "/health", nil); rr.Code != http.StatusOK {
		t.Fatalf("health after restart = %d", rr.Code)
	}
}

func TestEphemeralStateIsRemoved(t *testing.T) {
	scope := t.TempDir()
	_, closer, err := New(Options{Scope: scope})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dir := filepath.Dir(stateDBPath())
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("state dir missing while active: %v", err)
	}
	if err := closer(); err != nil {
		t.Fatalf("closer: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("ephemeral state dir survived close: %v", err)
	}
}

func TestPersistentStateDirAndSigningKey(t *testing.T) {
	scope := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	key := []byte("0123456789abcdef0123456789abcdef")
	_, closer, err := New(Options{Scope: scope, StateDir: stateDir, SigningKey: key})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := closer(); err != nil {
		t.Fatalf("closer: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "state.sqlite")); err != nil {
		t.Fatalf("persistent state missing after close: %v", err)
	}
	// Same key on the same store: accepted.
	_, closer, err = New(Options{Scope: scope, StateDir: stateDir, SigningKey: key})
	if err != nil {
		t.Fatalf("New with same key: %v", err)
	}
	_ = closer()
	// A different key against a store that already minted one is refused.
	_, closer, err = New(Options{Scope: scope, StateDir: stateDir, SigningKey: []byte("different-key-different-key-1234")})
	if err == nil {
		_ = closer()
		t.Fatalf("New with a differing key was accepted")
	}
}

func TestRejectsMissingScope(t *testing.T) {
	_, _, err := New(Options{Scope: filepath.Join(t.TempDir(), "nope")})
	if err == nil {
		t.Fatalf("New accepted a missing scope")
	}
}
