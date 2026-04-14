//go:build unix

package tail

import (
	"os"
	"syscall"
)

func inode(fi os.FileInfo) uint64 {
	if s, ok := fi.Sys().(*syscall.Stat_t); ok {
		return s.Ino
	}
	return 0
}
