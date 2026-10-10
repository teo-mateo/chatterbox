// Command chatterbox is an IRC client for AI coding agents.
package main

import (
	"os"

	"github.com/teo-mateo/chatterbox/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}))
}
