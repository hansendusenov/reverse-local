//go:build !windows

package admin

import (
	"os"
)

// IsElevated returns true if the process is running as root (UID 0) on Linux or macOS.
func IsElevated() bool {
	return os.Geteuid() == 0
}
