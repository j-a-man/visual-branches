// Command demo builds a realistic repository for trying vb, recording
// screenshots, and manual end-to-end testing.
//
//	go run ./tools/demo <dir>
//	cd <dir>/webapp && VB_GITHUB_FIXTURE=../prs.json vb
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/j-a-man/visual-branches/internal/demorepo"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./tools/demo <dir>")
		os.Exit(2)
	}
	root, err := filepath.Abs(os.Args[1])
	if err == nil {
		var res demorepo.Result
		res, err = demorepo.Build(root)
		if err == nil {
			fmt.Printf("demo ready:\n  cd %s\n  VB_GITHUB_FIXTURE=../prs.json vb\n", res.Repo)
			return
		}
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
