package main

import (
	"fmt"
	"os"

	"github.com/ShizukaJiku/conduit/internal/cli"

	// Plugin features self-register via blank import. Adding a feature =
	// add its package here; internal/cli never changes.
	_ "github.com/ShizukaJiku/conduit/internal/feature/download"
	_ "github.com/ShizukaJiku/conduit/internal/feature/watch"

	// Storage drivers self-register the same way. The shipped binary only
	// wires sftp (v1); memfs is test-only, s3 is post-v1.
	_ "github.com/ShizukaJiku/conduit/internal/storage/sftp"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
