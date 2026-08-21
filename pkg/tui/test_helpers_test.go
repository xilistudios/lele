package tui

import "errors"

// errForce constructs a simple error for error-path tests.
func errForce(s string) error { return errors.New(s) }