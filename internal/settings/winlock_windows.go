//go:build windows

package settings

import (
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	gdi32                = syscall.NewLazyDLL("gdi32.dll")
	procGetDesktopWindow = user32.NewProc("GetDesktopWindow")
	procGetWindow        = user32.NewProc("GetWindow")
	procIsWindow         = user32.NewProc("IsWindow")
	procIsWindowVisible  = user32.NewProc("IsWindowVisible")
	procIsZoomed         = user32.NewProc("IsZoomed")
	procGetWindowRect    = user32.NewProc("GetWindowRect")
	procGetWindowTextW   = user32.NewProc("GetWindowTextW")
	procGetWindowLongW   = user32.NewProc("GetWindowLongW")
	procSetWindowLongW   = user32.NewProc("SetWindowLongW")
	procGetSystemMenu    = user32.NewProc("GetSystemMenu")
	procDeleteMenu       = user32.NewProc("DeleteMenu")
	procEnableMenuItem   = user32.NewProc("EnableMenuItem")
	procSetWindowPos     = user32.NewProc("SetWindowPos")
	procShowWindow       = user32.NewProc("ShowWindow")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procMoveWindow       = user32.NewProc("MoveWindow")
	procSetLayeredAttr   = user32.NewProc("SetLayeredWindowAttributes")
	procGetStockObject   = gdi32.NewProc("GetStockObject")

	guardRunning atomic.Bool
	shieldMu     sync.Mutex
	shieldMap    = make(map[uintptr]uintptr) // parentHWND -> disabledMaximizeOverlayHWND
)

type winRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

const (
	gwChild        = 5
	gwHwndNext     = 2
	gwlStyle       = ^uintptr(15) // -16 (GWL_STYLE)
	gwlExStyle     = ^uintptr(19) // -20 (GWL_EXSTYLE)
	wsChild        = uintptr(0x40000000)
	wsVisible      = uintptr(0x10000000)
	wsThickFrame   = uintptr(0x00040000)
	wsMaximizeBox  = uintptr(0x00010000)
	wsExLayered    = uintptr(0x00080000)
	lwaAlpha       = uintptr(0x00000002)
	scSize         = uintptr(0xF000)
	scMaximize     = uintptr(0xF030)
	mfByCommand    = uintptr(0x00000000)
	mfDisabled     = uintptr(0x00000002)
	mfGrayed       = uintptr(0x00000001)
	swpNoMove      = uintptr(0x0002)
	swpNoZOrder    = uintptr(0x0004)
	swpFrameChange = uintptr(0x0020)
	swRestore      = uintptr(9)

	controlPanelWidth  = int32(480)
	controlPanelHeight = int32(660)
)

// EnforceFixedControlPanelWindow runs a persistent Win32 supervisor that:
// 1. Strips WS_MAXIMIZEBOX and WS_THICKFRAME from the Control Panel window,
// 2. Disables SC_MAXIMIZE and SC_SIZE in the window's system menu,
// 3. Places a semi-transparent child hit-shield directly over the Chromium [□] Maximize caption button
//    so the button is visually dimmed (grayed out) and completely unclickable, and
// 4. Locks the window dimensions to 480x660px.
func EnforceFixedControlPanelWindow() {
	if !guardRunning.CompareAndSwap(false, true) {
		return
	}

	go func() {
		defer guardRunning.Store(false)

		for {
			found := lockMatchingControlPanelWindows()
			if found {
				time.Sleep(50 * time.Millisecond)
			} else {
				time.Sleep(250 * time.Millisecond)
			}
		}
	}()
}

func lockMatchingControlPanelWindows() bool {
	desktop, _, _ := procGetDesktopWindow.Call()
	if desktop == 0 {
		return false
	}

	hwnd, _, _ := procGetWindow.Call(desktop, gwChild)
	foundAny := false
	buf := make([]uint16, 256)

	for hwnd != 0 {
		vis, _, _ := procIsWindowVisible.Call(hwnd)
		if vis != 0 {
			n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
			if n > 0 {
				title := strings.ToLower(syscall.UTF16ToString(buf[:n]))
				if strings.Contains(title, "desktop control panel") ||
					(strings.Contains(title, "reverse.local") && strings.Contains(title, "control panel")) {
					enforceFixedWindowOnHWND(hwnd)
					foundAny = true
				}
			}
		}
		hwnd, _, _ = procGetWindow.Call(hwnd, gwHwndNext)
	}

	return foundAny
}

func enforceFixedWindowOnHWND(hwnd uintptr) {
	// 1. If the window was maximized (zoomed), immediately restore it
	zoomed, _, _ := procIsZoomed.Call(hwnd)
	if zoomed != 0 {
		_, _, _ = procShowWindow.Call(hwnd, swRestore)
	}

	// 2. Strip WS_THICKFRAME and WS_MAXIMIZEBOX whenever present
	style, _, _ := procGetWindowLongW.Call(hwnd, gwlStyle)
	styleChanged := false
	if style != 0 && (style&(wsThickFrame|wsMaximizeBox)) != 0 {
		newStyle := style &^ (wsThickFrame | wsMaximizeBox)
		_, _, _ = procSetWindowLongW.Call(hwnd, gwlStyle, newStyle)
		styleChanged = true
	}

	hMenu, _, _ := procGetSystemMenu.Call(hwnd, 0)
	if hMenu != 0 {
		_, _, _ = procEnableMenuItem.Call(hMenu, scMaximize, mfByCommand|mfDisabled|mfGrayed)
		_, _, _ = procEnableMenuItem.Call(hMenu, scSize, mfByCommand|mfDisabled|mfGrayed)
		_, _, _ = procDeleteMenu.Call(hMenu, scSize, mfByCommand)
		_, _, _ = procDeleteMenu.Call(hMenu, scMaximize, mfByCommand)
	}

	// 3. Ensure the [□] Maximize caption button hit-shield child window is attached and positioned
	ensureMaximizeButtonDisabledOverlay(hwnd)

	// 4. Lock dimensions to 480x660
	var r winRect
	ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	width := r.Right - r.Left
	height := r.Bottom - r.Top

	if styleChanged || ret == 0 || width != controlPanelWidth || height != controlPanelHeight {
		_, _, _ = procSetWindowPos.Call(
			hwnd,
			0,
			0,
			0,
			uintptr(controlPanelWidth),
			uintptr(controlPanelHeight),
			swpNoMove|swpNoZOrder|swpFrameChange,
		)
	}
}

// ensureMaximizeButtonDisabledOverlay attaches a 46x31 child STATIC overlay window directly over
// Chromium's custom-drawn [□] Maximize caption button at (width - 92, 1) so it looks grayed-out
// and absorbs all mouse hover and click events.
func ensureMaximizeButtonDisabledOverlay(parentHwnd uintptr) {
	shieldMu.Lock()
	defer shieldMu.Unlock()

	const overlayX = int32(controlPanelWidth - 92) // Between Minimize [-138..-92] and Close [-46..0]
	const overlayY = int32(1)
	const overlayW = int32(46)
	const overlayH = int32(31)

	if child, ok := shieldMap[parentHwnd]; ok && child != 0 {
		valid, _, _ := procIsWindow.Call(child)
		if valid != 0 {
			_, _, _ = procMoveWindow.Call(
				child,
				uintptr(overlayX),
				uintptr(overlayY),
				uintptr(overlayW),
				uintptr(overlayH),
				1,
			)
			return
		}
		delete(shieldMap, parentHwnd)
	}

	className, _ := syscall.UTF16PtrFromString("STATIC")
	windowName, _ := syscall.UTF16PtrFromString("")

	childHwnd, _, _ := procCreateWindowExW.Call(
		wsExLayered,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowName)),
		wsChild|wsVisible,
		uintptr(overlayX),
		uintptr(overlayY),
		uintptr(overlayW),
		uintptr(overlayH),
		parentHwnd,
		0,
		0,
		0,
	)

	if childHwnd != 0 {
		// 175/255 (~68% opacity) dims the underlying [□] glyph so it visibly looks disabled/grayed out
		// while still intercepting 100% of mouse hover and click events.
		_, _, _ = procSetLayeredAttr.Call(childHwnd, 0, 175, lwaAlpha)
		shieldMap[parentHwnd] = childHwnd
	}
}
