//go:build !tenvad_native || !linux || !amd64 || !cgo

package ten

import (
	"errors"

	"github.com/cavos-io/rtp-agent/core/vad"
)

func newNativeProbabilityEstimatorFactory(VADOptions) (vad.ProbabilityEstimatorFactory, error) {
	return nil, errors.New("TEN VAD native library integration is not enabled")
}
