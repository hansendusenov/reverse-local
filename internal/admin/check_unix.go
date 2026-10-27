//go:build !windows

package admin

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// IsElevated returns true if the process is running as root (UID 0) on Linux or macOS.
func IsElevated() bool {
	return os.Geteuid() == 0
}

// RelaunchElevated prompts for administrator/root privileges on macOS (via osascript) or pkexec on Linux.
func RelaunchElevated() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	var args []string
	for _, a := range os.Args[1:] {
		if a == "-elevated-relaunch" || a == "--elevated-relaunch" {
			continue
		}
		args = append(args, fmt.Sprintf("%q", a))
	}
	args = append(args, "-elevated-relaunch")

	switch runtime.GOOS {
	case "darwin":
		shellCmd := fmt.Sprintf("%q %s >/dev/null 2>&1 &", exePath, strings.Join(args, " "))
		script := fmt.Sprintf("do shell script %q with administrator privileges", shellCmd)
		cmd := exec.Command("osascript", "-e", script)
		return cmd.Run()
	default:
		if pkexec, err := exec.LookPath("pkexec"); err == nil {
			cmdArgs := append([]string{exePath}, os.Args[1:]...)
			cmdArgs = append(cmdArgs, "-elevated-relaunch")
			cmd := exec.Command(pkexec, cmdArgs...)
			return cmd.Start()
		}
		return fmt.Errorf("root privileges required; please run with sudo")
	}
}

