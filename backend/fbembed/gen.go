package fbembed

// Regenerate the embedded frontend after any frontend change:
//
//	go generate ./fbembed
//
// gen.sh builds ../frontend (npm) into internal/web/embed and repacks the
// dist.zip embedded by assets.go. dist.zip is committed so importers build
// with only the Go toolchain (no Node).
//
//go:generate bash gen.sh
