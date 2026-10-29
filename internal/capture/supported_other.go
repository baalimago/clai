//go:build !linux && !darwin

package capture

import "context"

func sources(context.Context, string) ([]Source, error) { return nil, ErrUnsupported }
