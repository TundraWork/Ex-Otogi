//go:build linux

package store

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
	return validateFilesystemType(int64(stats.Type))
}

func validateFilesystemType(fsType int64) error {
	switch uint64(fsType) {
	case nfsSuperMagic, smbSuperMagic, cifsSuperMagic, ninePSuperMagic, cephSuperMagic:
		return fmt.Errorf("network filesystem type %#x is not supported", fsType)
	default:
		return nil
	}
}
