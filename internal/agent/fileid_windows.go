//go:build windows

package agent

import (
	"fmt"
	"os"
	"syscall"
)

// fileID identifies the file behind f across renames (volume serial + file index).
func fileID(f *os.File) string {
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &info); err != nil {
		return ""
	}
	return fmt.Sprintf("%x:%x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)
}
