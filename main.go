package main

import (
	"os"

	"github.com/baalimago/clai/internal/cli"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

func main() {
	ancli.SetupSlog()
	os.Exit(cli.RunProfiled(os.Args[1:], cli.DefaultDeps()))
}
