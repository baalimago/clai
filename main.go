package main

import (
	"os"

	"github.com/baalimago/clai/internal/cli"
)

func main() {
	deps := cli.DefaultDeps()
	deps.ManPage = cli.FirstRunManPage()
	os.Exit(cli.RunProfiled(os.Args[1:], deps))
}
