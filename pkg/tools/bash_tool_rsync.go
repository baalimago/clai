package tools

import (
	"fmt"
	"os/exec"
	"strings"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type RsyncTool pub_models.Specification

var Rsync = RsyncTool{
	Name:        "rsync",
	Description: "Synchronize files or directories with archive and partial-transfer modes. Remote paths require explicit opt-in.",
	Inputs: &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
			"source": {
				Type:        "string",
				Description: "The source path. A trailing slash copies the directory contents instead of the directory itself.",
			},
			"destination": {
				Type:        "string",
				Description: "The destination path.",
			},
			"allow_remote": {
				Type:        "boolean",
				Description: "Allow a remote source or destination. Defaults to false.",
			},
			"delete": {
				Type:        "boolean",
				Description: "Delete destination entries that do not exist at the source. Defaults to false.",
			},
			"dry_run": {
				Type:        "boolean",
				Description: "Show changes without applying them. Defaults to false.",
			},
		},
		Required: []string{"source", "destination"},
	},
}

func (r RsyncTool) Call(input pub_models.Input) (string, error) {
	source, ok := input["source"].(string)
	if !ok || source == "" {
		return "", fmt.Errorf("source must be a non-empty string")
	}
	destination, ok := input["destination"].(string)
	if !ok || destination == "" {
		return "", fmt.Errorf("destination must be a non-empty string")
	}
	allowRemote, err := optionalBool(input, "allow_remote")
	if err != nil {
		return "", err
	}
	if !allowRemote && (isRsyncRemotePath(source) || isRsyncRemotePath(destination)) {
		return "", fmt.Errorf("remote paths require allow_remote to be true")
	}
	deleteExtraneous, err := optionalBool(input, "delete")
	if err != nil {
		return "", err
	}
	dryRun, err := optionalBool(input, "dry_run")
	if err != nil {
		return "", err
	}

	args := []string{"--archive", "--partial"}
	if deleteExtraneous {
		args = append(args, "--delete")
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	args = append(args, "--", source, destination)
	output, err := exec.Command("rsync", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("rsync %q to %q: %w, output: %s", source, destination, err, output)
	}
	return string(output), nil
}

func optionalBool(input pub_models.Input, name string) (bool, error) {
	if input[name] == nil {
		return false, nil
	}
	value, ok := input[name].(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", name)
	}
	return value, nil
}

func isRsyncRemotePath(path string) bool {
	if strings.Contains(path, "://") {
		return true
	}
	colon := strings.IndexByte(path, ':')
	slash := strings.IndexByte(path, '/')
	return colon >= 0 && (slash < 0 || colon < slash)
}

func (r RsyncTool) Specification() pub_models.Specification {
	return pub_models.Specification(Rsync)
}
