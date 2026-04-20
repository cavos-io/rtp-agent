package turndetector

import (
	"context"
	"fmt"
)

// TurnDetector defines the interface for an end-of-utterance or turn detection model.
type TurnDetector interface {
	Detect(ctx context.Context, text string) (bool, error)
}

type EOUPredictor struct {
	Model string
}

func NewEOUPredictor(model string) *EOUPredictor {
	if model == "" {
		model = "turn-detector-default"
	}
	return &EOUPredictor{Model: model}
}

func (e *EOUPredictor) Detect(ctx context.Context, text string) (bool, error) {
	return false, fmt.Errorf("local turn detector onnx models not natively supported in go port yet")
}

