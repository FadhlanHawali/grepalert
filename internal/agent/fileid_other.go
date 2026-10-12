//go:build !unix && !windows

package agent

import "os"

// fileID is unavailable here; resuming relies on the content fingerprint alone.
func fileID(*os.File) string { return "" }
