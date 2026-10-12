//go:build unix

package agent

import (
	"fmt"
	"os"
	"syscall"
)

// fileID identifies the file behind f across renames (device + inode).
func fileID(f *os.File) string {
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%x:%x", uint64(st.Dev), uint64(st.Ino))
}
