//go:build !windows

package settings

// EnforceFixedControlPanelWindow is a no-op on non-Windows platforms where --window-size governs the app frame.
func EnforceFixedControlPanelWindow() {}

