//go:build windows

package admin

import (
	"golang.org/x/sys/windows"
)

// IsElevated returns true if the current Windows process is running with elevated Administrator privileges.
func IsElevated() bool {
	token := windows.GetCurrentProcessToken()
	return token.IsElevated()
}
