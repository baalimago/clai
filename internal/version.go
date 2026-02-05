package internal

import (
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
)

// Set with buildflag if built in pipeline and not using go install
var (
	BuildVersion  = ""
	BuildChecksum = ""
)

func printVersion() (models.Querier, error) {
	hasPrintedVersion := false
	if BuildVersion != "" {
		hasPrintedVersion = true
		fmt.Println("version: " + BuildVersion)
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return nil, errors.New("failed to read build info")
	}
	if !hasPrintedVersion {
		fmt.Println("version: " + bi.Main.Version)
	}
	for _, dep := range bi.Deps {
		fmt.Printf("%s %s\n", dep.Path, dep.Version)
	}
	return nil, utils.ErrUserInitiatedExit
}
