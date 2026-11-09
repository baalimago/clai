package main

import (
	"os"

	"github.com/baalimago/clai/internal/cli"
)

func main() {
	os.Exit(cli.RunProfiled(os.Args[1:], cli.DefaultDeps()))
}
