package utils

import "errors"

var (
	ErrUserInitiatedExit = errors.New("user exit")
	ErrBack              = errors.New("back")
)
