//go:build linux

package store

import "testing"

func TestFilesystemTypeAllowlistFailsClosed(t *testing.T) {
	for name, fsType := range map[string]int64{
		"nfs":     0x6969,
		"smb":     0x517B,
		"cifs":    0xFF534D42,
		"9p":      0x01021997,
		"ceph":    0x00C36400,
		"fuse":    0x65735546,
		"unknown": 0x12345678,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateFilesystemType(fsType); err == nil {
				t.Fatalf("filesystem type %#x accepted", fsType)
			}
		})
	}
	for name, fsType := range map[string]int64{"ext": extSuperMagic, "xfs": xfsSuperMagic, "btrfs": btrfsSuperMagic, "tmpfs": tmpfsSuperMagic, "zfs": zfsSuperMagic, "f2fs": f2fsSuperMagic, "ubifs": ubifsSuperMagic, "overlay": overlaySuperMagic} {
		t.Run(name, func(t *testing.T) {
			if err := validateFilesystemType(fsType); err != nil {
				t.Fatalf("local filesystem rejected: %v", err)
			}
		})
	}
}
