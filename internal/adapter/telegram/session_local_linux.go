//go:build linux

package telegram

import (
	"fmt"
	"syscall"
)

const (
	nfsSuperMagic   = 0x6969
	smbSuperMagic   = 0x517B
	cifsSuperMagic  = 0xFF534D42
	ninePSuperMagic = 0x01021997
	cephSuperMagic  = 0x00C36400
)

func validateLocalFilesystem(path string) error {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(path, &stats); err != nil {
		return fmt.Errorf("inspect filesystem: %w", err)
	}
	switch uint64(stats.Type) {
	case nfsSuperMagic, smbSuperMagic, cifsSuperMagic, ninePSuperMagic, cephSuperMagic:
		return fmt.Errorf("network filesystem type %#x is not supported", stats.Type)
	default:
		return nil
	}
}
