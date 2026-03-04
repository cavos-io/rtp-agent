package durable

import (
	"errors"
)

// ErrNotSupported is returned because Go does not support serializing/deserializing execution frames like Python.
var ErrNotSupported = errors.New("durable execution (frame serialization) is not natively supported in Go")

// For structural parity, we expose an error indicating this is a Python-specific plugin feature.
func Init() error {
	return ErrNotSupported
}
