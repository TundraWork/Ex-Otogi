//go:build linux

package telegram

import (
	"errors"
	"fmt"
	"os"
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

func validatePathOwnerAndLinks(info os.FileInfo, directory bool) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("inspect path ownership")
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return errors.New("path must be owned by current user")
	}
	if !directory && stat.Nlink != 1 {
		return fmt.Errorf("file link count is %d, want 1", stat.Nlink)
	}
	return nil
}
