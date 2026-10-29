//go:build !linux && !darwin

package capture

import "context"

type unsupportedRecorder struct{}

func newRecorder() Recorder { return unsupportedRecorder{} }

func (unsupportedRecorder) Sources(ctx context.Context) ([]Source, error) {
	return nil, ErrUnsupported
}

func (unsupportedRecorder) Record(ctx context.Context, req Request) (Recording, error) {
	return Recording{}, ErrUnsupported
}
