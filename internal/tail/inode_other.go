//go:build !unix

package tail

import "os"

// On platforms where we can't cheaply get an inode (Windows), rotation
// detection via inode comparison is disabled.
func inode(_ os.FileInfo) uint64 { return 0 }
