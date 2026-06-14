package livekit

import (
	"fmt"

	"github.com/sugarme/tokenizer/pretrained"
)

func NewHuggingFaceTurnDetectorTokenizer(modelType ModelType, tokenizerPath string) (TurnDetectorTokenizer, error) {
	tk, err := pretrained.FromFile(tokenizerPath)
	if err != nil {
		return nil, fmt.Errorf("load livekit turn detector tokenizer: %w", err)
	}
	return newTurnDetectorTokenizer(modelType, func(text string) ([]int, error) {
		encoding, err := tk.EncodeSingle(text, false)
		if err != nil {
			return nil, err
		}
		return encoding.Ids, nil
	}), nil
}
