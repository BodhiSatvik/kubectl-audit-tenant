//go:build windows

package report

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVT switches the console to virtual-terminal processing so ANSI colors render.
func enableVT(f *os.File) bool {
	h := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return false
	}
	return windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
