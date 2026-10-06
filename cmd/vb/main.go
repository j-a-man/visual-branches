// Command vb shows a map of your git branches for humans and coding agents.
package main

import (
	"os"

	"github.com/j-a-man/visual-branches/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
