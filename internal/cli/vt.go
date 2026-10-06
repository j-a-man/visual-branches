package cli

import (
	"os"

	"charm.land/lipgloss/v2"
)

// enableVT turns on ANSI escape processing for legacy Windows consoles. It
// is a no-op elsewhere.
func enableVT(f *os.File) {
	lipgloss.EnableLegacyWindowsANSI(f)
}
