// Package datadir is where Algebra keeps local state that has to survive a
// restart but isn't in the database: wallet key files, linked merchant
// sessions, tool manifests.
//
// It is ".data" relative to the working directory unless ALGEBRA_DATA_DIR says
// otherwise. The dev scripts set it to the repository's own .data, so the
// backend can be run from its own folder; a deployment points it at a
// persistent volume.
package datadir

import (
	"os"
	"path/filepath"
)

// Env names the variable that overrides the directory.
const Env = "ALGEBRA_DATA_DIR"

// Dir is the data directory.
func Dir() string {
	if v := os.Getenv(Env); v != "" {
		return v
	}
	return ".data"
}

// Path joins elements under Dir.
func Path(elem ...string) string {
	return filepath.Join(append([]string{Dir()}, elem...)...)
}
