//go:build linux

package store

import (
	"fmt"
	"os"
	"syscall"
)

const (
	extSuperMagic     = 0xEF53
	xfsSuperMagic     = 0x58465342
	btrfsSuperMagic   = 0x9123683E
	tmpfsSuperMagic   = 0x01021994
	zfsSuperMagic     = 0x2FC12FC1
	f2fsSuperMagic    = 0xF2F52010
	ubifsSuperMagic   = 0x24051905
	overlaySuperMagic = 0x794C7630
)

func validateLocalFilesystem(path string) error {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(path, &stats); err != nil {
		return fmt.Errorf("inspect filesystem: %w", err)
	}
	return validateFilesystemType(int64(stats.Type))
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func hasSingleLink(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}

func validateFilesystemType(fsType int64) error {
	switch uint64(fsType) {
	case extSuperMagic, xfsSuperMagic, btrfsSuperMagic, tmpfsSuperMagic, zfsSuperMagic, f2fsSuperMagic, ubifsSuperMagic, overlaySuperMagic:
		return nil
	default:
		return fmt.Errorf("filesystem type %#x is not positively identified as local", fsType)
	}
}
