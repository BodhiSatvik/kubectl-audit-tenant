package report

import (
	"io"
	"os"

	"golang.org/x/term"
)

const reset = "\x1b[0m"

var colors = map[Verdict]string{Pass: "\x1b[32m", Fail: "\x1b[31m", Skip: "\x1b[33m", Error: "\x1b[35m"}

// WantColor is true when w is a terminal, NO_COLOR is unset, --no-color is absent and, on
// Windows, the console accepts escape codes.
func WantColor(w io.Writer, noColor bool) bool {
	if noColor || os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd())) && enableVT(f)
}

// Width is the terminal width of w, or 0 when w is not a terminal.
func Width(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok {
		return 0
	}
	width, _, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0
	}
	return width
}
