package text

import "context"

type CallUsageRecorder interface {
	Record(context.Context, CompletedModelCall) error
}

type noopCallUsageRecorder struct{}

func (noopCallUsageRecorder) Record(context.Context, CompletedModelCall) error {
	return nil
}
