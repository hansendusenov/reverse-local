//go:build windows

package admin

import (
	"golang.org/x/sys/windows"
)

// IsElevated returns true if the current Windows process is running with elevated Administrator privileges.
// Note: Automatic UAC elevation on click is declared natively in the PE application manifest
// (rsrc_windows_amd64.syso -> requestedExecutionLevel level="requireAdministrator").
func IsElevated() bool {
	token := windows.GetCurrentProcessToken()
	return token.IsElevated()
}

// RelaunchElevated is a no-op when the PE manifest already enforces requireAdministrator via the OS loader.
func RelaunchElevated() error {
	return nil
}
