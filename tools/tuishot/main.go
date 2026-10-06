// Command tuishot prints one frame of `vb tui` with colors, for screenshots.
//
//	go run ./tools/tuishot -C <repo> -w 132 -h 30 -select feat/auth-ui | go run ./tools/termshot > tui.svg
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/charmbracelet/colorprofile"

	"github.com/j-a-man/visual-branches/internal/engine"
	"github.com/j-a-man/visual-branches/internal/tui"
)

func main() {
	dir := flag.String("C", ".", "repository")
	width := flag.Int("w", 132, "terminal width")
	height := flag.Int("h", 30, "terminal height")
	selected := flag.String("select", "", "branch to select (default: current)")
	flag.Parse()
	ctx := context.Background()
	s, err := engine.Open(ctx, engine.Options{Dir: *dir})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	frame, err := tui.Snapshot(ctx, tui.Options{Session: s}, *width, *height, *selected)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	w := &colorprofile.Writer{Forward: os.Stdout, Profile: colorprofile.TrueColor}
	fmt.Fprintln(w, frame)
}
