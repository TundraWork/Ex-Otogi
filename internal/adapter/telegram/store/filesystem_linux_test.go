//go:build linux

package store

import "testing"

func TestKnownNetworkFilesystemTypesAreRejected(t *testing.T) {
	for name, fsType := range map[string]int64{
		"nfs":  0x6969,
		"smb":  0x517B,
		"cifs": 0xFF534D42,
		"9p":   0x01021997,
		"ceph": 0x00C36400,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateFilesystemType(fsType); err == nil {
				t.Fatalf("filesystem type %#x accepted", fsType)
			}
		})
	}
	if err := validateFilesystemType(0xEF53); err != nil {
		t.Fatalf("local filesystem rejected: %v", err)
	}
}
