// Command spec-kitty is a Go implementation of the Spec Kitty CLI.
package main

import (
	"os"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
)

func main() {
	os.Exit(cli.Execute(app.Root(), os.Args[1:], cli.IO{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}))
}
