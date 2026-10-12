//go:build !windows

package agent

import "os"

func openShared(path string) (*os.File, error) { return os.Open(path) }
