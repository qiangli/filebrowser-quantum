package fbembed

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"fmt"
	"io/fs"
)

// distZip is the built frontend (backend/internal/web/embed after
// `npm run build`), zipped and committed so an importer gets the UI without a
// Node toolchain and without depending on internal/web's go:embed of an
// untracked directory. Regenerate after any frontend change with
// `go generate ./fbembed` (see gen.go).
//
//go:embed dist.zip
var distZip []byte

// distFS decodes the embedded frontend into an fs.FS rooted at the dist
// contents, so "public/index.html", "assets/...", "fonts/..." resolve the way
// fs.Sub(web.GetEmbeddedAssets(), "embed") would. *zip.Reader satisfies
// fs.FS, and the static handler reads whole files (fs.ReadFile), so
// compressed entries need no seeking.
func distFS() (fs.FS, error) {
	zr, err := zip.NewReader(bytes.NewReader(distZip), int64(len(distZip)))
	if err != nil {
		return nil, fmt.Errorf("fbembed: open embedded dist.zip: %w", err)
	}
	return zr, nil
}

// assetsSource is the asset FS New() serves from. It is a variable solely so
// tests can inject a wrapper. Production always uses distFS.
var assetsSource = distFS
