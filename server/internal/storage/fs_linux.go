//go:build linux

package storage

import (
	"fmt"
	"path/filepath"
	"syscall"
)

// Filesystem magic numbers from statfs(2) that must not hold a WAL database.
// FUSE is allowed: it also covers local tools such as Unraid's /mnt/user.
var networkFS = map[int64]string{
	0x6969:     "nfs",
	0x517b:     "smb",
	0xff534d42: "cifs",
	0xfe534d42: "smb2",
	0x01021997: "9p",
	0x00c36400: "ceph",
}

func checkLocalFilesystem(dbPath string) error {
	var st syscall.Statfs_t
	if err := syscall.Statfs(filepath.Dir(dbPath), &st); err != nil {
		return fmt.Errorf("storage: check %s: %w", filepath.Dir(dbPath), err)
	}
	if name, bad := networkFS[int64(st.Type)]; bad {
		return fmt.Errorf("%w (%s)", ErrNetworkFilesystem, name)
	}
	return nil
}
