package store

import "errors"

var ErrDuplicate = errors.New("store: duplicate key")

var ErrUnsupportedPlatform = errors.New("store: unsupported platform")
