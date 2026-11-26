//go:build !unix

package watch

import "os"

// lockFile is a no-op where flock is unavailable; the watcher is expected to
// run under a single supervisor there.
func lockFile(*os.File) error { return nil }
