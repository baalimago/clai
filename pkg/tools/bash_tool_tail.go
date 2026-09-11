package tools

import (
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type TailTool pub_models.Specification

var Tail = TailTool{
	Name:        "tail",
	Description: "Display the last part of a file. Uses the system command 'tail'. Follow mode is not exposed.",
	Inputs:      countedFileInputs(),
}

func (t TailTool) Call(input pub_models.Input) (string, error) {
	return runCountedFileTool("tail", input)
}

func (t TailTool) Specification() pub_models.Specification {
	return pub_models.Specification(Tail)
}
