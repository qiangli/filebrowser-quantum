// Package fbembed lets another Go program embed File Browser in-process: one
// call returns a ready http.Handler with no CLI, no login surface, and a
// read-only/download-only default. It is the embedding seam the outpost
// `files` builtin and the yoke web console files panel use, but it is
// generic — any host process can mount the returned handler under its own
// listener or reverse proxy.
//
// The embed is single-user (noauth) and the embedding host is the access
// gate, exactly as it is for a shell or ssh surface. Scope and write mode come
// from Options on every boot. The upstream engine keeps its state (the lone
// user record, session-token registry, the file index, the preview cache) in
// SQLite under Options.StateDir; when StateDir is empty a temporary directory
// is used and removed by the closer, so nothing persists across boots.
//
// The handler speaks the cooperative-web-app prefix contract: the page is
// rendered against X-Forwarded-Prefix when a path-mounting proxy sends it, so
// the same instance works on loopback and behind a prefixing proxy with no
// per-mount config.
//
// File Browser configures itself through package-level state (settings.Config,
// the state store, the index registry), so only one embedded instance can be
// active per process. New returns ErrActive while a previous instance is
// open; its closer tears everything down and a later New starts clean.
package fbembed

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/gtsteffaniak/filebrowser/backend/internal/adapters/fs/fileutils"
	"github.com/gtsteffaniak/filebrowser/backend/internal/app"
	"github.com/gtsteffaniak/filebrowser/backend/internal/events"
	"github.com/gtsteffaniak/filebrowser/backend/internal/icons"
	"github.com/gtsteffaniak/filebrowser/backend/internal/preview"
	"github.com/gtsteffaniak/filebrowser/backend/internal/state"
	"github.com/gtsteffaniak/filebrowser/backend/internal/utils"
	"github.com/gtsteffaniak/filebrowser/backend/internal/web"
	"github.com/gtsteffaniak/filebrowser/backend/pkg/indexing"
	"github.com/gtsteffaniak/filebrowser/backend/pkg/settings"
)

// ErrActive is returned by New while a previous embedded instance has not
// been closed. File Browser's configuration and stores are process-global.
var ErrActive = errors.New("fbembed: an instance is already active; close it first")

// SourceName is the name of the single source the embed serves. The SPA shows
// it as the root of the file tree.
const SourceName = "files"

// Options configures an embedded File Browser instance.
type Options struct {
	// Scope is the filesystem root the browser is confined to. Defaults to
	// the current working directory when empty.
	Scope string

	// AllowWrite enables every write operation together — create/upload,
	// modify/edit, rename, delete, archive. When false (the default) the
	// source is read-only and download-only. Command execution and sharing
	// are never enabled here.
	AllowWrite bool

	// SigningKey is the JWT signing key. When non-empty (the host persists
	// one out of band, e.g. in its own config) sessions survive a process
	// restart even with an ephemeral StateDir. When empty the engine mints a
	// key per state database; clients re-auth transparently since noauth has
	// no login prompt. With a persistent StateDir the key stored there wins
	// and a differing SigningKey is an error.
	SigningKey []byte

	// ImageProcessors is the preview worker count. Defaults to 4.
	ImageProcessors int

	// StateDir holds the engine's SQLite state and index databases and its
	// preview cache. Empty means a fresh temporary directory per New that
	// the closer removes.
	StateDir string

	// Title is the page/app title. Defaults to "Files".
	Title string
}

var (
	mu     sync.Mutex
	active bool
)

// New returns an embedded File Browser http.Handler and its closer. The
// closer stops the index scanners, closes the databases and removes an
// ephemeral StateDir; call it before starting another instance.
//
// Auth is noauth — the embedding host is the access gate. The lone user is an
// admin (so the settings surface works) but the source is read-only unless
// AllowWrite is set, and the engine enforces read-only ahead of user perms.
func New(opts Options) (handler http.Handler, closer func() error, err error) {
	mu.Lock()
	defer mu.Unlock()
	if active {
		return nil, nil, ErrActive
	}

	if opts.Scope == "" {
		if opts.Scope, err = os.Getwd(); err != nil {
			return nil, nil, fmt.Errorf("fbembed: resolve scope: %w", err)
		}
	}
	scope, err := filepath.Abs(opts.Scope)
	if err != nil {
		return nil, nil, fmt.Errorf("fbembed: abs scope: %w", err)
	}
	if st, err := os.Stat(scope); err != nil || !st.IsDir() {
		return nil, nil, fmt.Errorf("fbembed: scope %q is not a directory", scope)
	}
	if opts.ImageProcessors < 1 {
		opts.ImageProcessors = 4
	}
	if opts.Title == "" {
		opts.Title = "Files"
	}

	stateDir, ephemeral := opts.StateDir, false
	if stateDir == "" {
		if stateDir, err = os.MkdirTemp("", "fbembed-*"); err != nil {
			return nil, nil, fmt.Errorf("fbembed: state dir: %w", err)
		}
		ephemeral = true
	} else if stateDir, err = filepath.Abs(stateDir); err != nil {
		return nil, nil, fmt.Errorf("fbembed: abs state dir: %w", err)
	}
	removeState := func() {
		if ephemeral {
			_ = os.RemoveAll(stateDir)
		}
	}
	fail := func(stage string, err error) (http.Handler, func() error, error) {
		removeState()
		return nil, nil, fmt.Errorf("fbembed: %s: %w", stage, err)
	}

	err = settings.InitializeEmbedded(func(c *settings.Settings) {
		configure(c, scope, stateDir, opts)
	})
	if err != nil {
		return fail("configure", err)
	}
	if err := utils.SetInvalidPasswordHash(); err != nil {
		return fail("security hash", err)
	}

	store, _, err := state.Open(settings.Config.Server.DatabaseV2.Path)
	if err != nil {
		return fail("open state", err)
	}
	closeState := func() {
		_ = state.Close()
		removeState()
	}
	a, err := app.WireServices(store)
	if err != nil {
		closeState()
		return nil, nil, fmt.Errorf("fbembed: wire services: %w", err)
	}
	if err := hardenUser(); err != nil {
		closeState()
		return nil, nil, fmt.Errorf("fbembed: lone user: %w", err)
	}

	isNewDb, err := indexing.InitializeIndexDB()
	if err != nil {
		closeState()
		return nil, nil, fmt.Errorf("fbembed: index db: %w", err)
	}
	for _, source := range settings.Config.Server.SourceMap {
		indexing.Initialize(source, false, isNewDb)
	}

	assets, err := assetsSource()
	if err != nil {
		closeIndex()
		closeState()
		return nil, nil, err
	}
	fileutils.InitAssetFS(assets, true)
	if err := preview.StartPreviewGenerator(opts.ImageProcessors, settings.Config.Server.CacheDir); err != nil {
		closeIndex()
		closeState()
		return nil, nil, fmt.Errorf("fbembed: preview service: %w", err)
	}
	// PWA icons are best-effort decorations; the static handler falls back
	// to the embedded favicon when they are missing.
	_ = icons.GeneratePWAIcons()
	icons.InitializePWAManifest()

	h, err := web.NewHandler(web.Deps{Store: a.Store, Files: a.Files, Auth: a.Auth})
	if err != nil {
		closeIndex()
		closeState()
		return nil, nil, fmt.Errorf("fbembed: build handler: %w", err)
	}

	active = true
	closer = func() error {
		mu.Lock()
		defer mu.Unlock()
		if !active {
			return nil
		}
		active = false
		events.Shutdown()
		closeIndex()
		preview.StopPreviewGenerator()
		err := state.Close()
		removeState()
		return err
	}
	return h, closer, nil
}

// closeIndex stops the index scanners and closes the index database.
func closeIndex() {
	indexing.StopAllScanners()
	if db := indexing.GetIndexDB(); db != nil {
		db.Close()
	}
}

// configure turns Options into the engine's settings: one source, noauth,
// no WebDAV, no sharing, quiet logging. (This fork has no update check and
// no analytics reporter to disable: both were removed.)
func configure(c *settings.Settings, scope, stateDir string, opts Options) {
	c.Server.Sources = []*settings.Source{{
		Path: scope,
		Name: SourceName,
		Config: settings.SourceConfig{
			DefaultEnabled:   true,
			DefaultUserScope: "/",
			ReadOnly:         !opts.AllowWrite,
			Private:          true,
		},
	}}
	c.Server.CacheDir = filepath.Join(stateDir, "cache")
	c.Server.DatabaseV2.Path = filepath.Join(stateDir, "state.sqlite")
	c.Server.NumImageProcessors = opts.ImageProcessors
	c.Server.Logging = []settings.LogConfig{{
		Output:    "stdout",
		Levels:    "error|warning",
		ApiLevels: "error",
		NoColors:  true,
	}}

	c.Auth.Methods.NoAuth = true
	c.Auth.Methods.PasswordAuth.Enabled = false
	c.Auth.Methods.PasswordAuth.Signup = false
	if len(opts.SigningKey) > 0 {
		c.Auth.Key = hex.EncodeToString(opts.SigningKey)
	}

	c.Http.BaseURL = ""
	c.Http.DisableWebDAV = true

	c.Frontend.Name = opts.Title
	c.Frontend.Description = "Browse and manage files on this host"
	c.Frontend.DisableDefaultLinks = true
	c.Frontend.DisablePWAInstall = true

	c.UserDefaults.Account.Permissions.Share = false
	c.UserDefaults.Account.Permissions.Api = false
}

// hardenUser trims the lone user's global perms: sharing and API tokens stay
// off, admin stays on so the settings surface works. The source itself is
// read-only unless AllowWrite, which the engine enforces before user perms.
func hardenUser() error {
	u, err := state.ResolveNoAuthUser()
	if err != nil {
		return err
	}
	if !u.Permissions.Share && !u.Permissions.Api && u.Permissions.Admin {
		return nil
	}
	u.Permissions.Share = false
	u.Permissions.Api = false
	u.Permissions.Admin = true
	return state.UpdateUser(&u, "", "permissions")
}

// stateDBPath reports the active instance's state database path (tests).
func stateDBPath() string { return settings.Config.Server.DatabaseV2.Path }
