package nvidia

import rivapb "github.com/cavos-io/rtp-agent/adapter/nvidia/internal/rivapb"

func nvidiaSTTStreamingConfig(s *NvidiaSTT, language string) *rivapb.StreamingRecognitionConfig {
	cfg := &rivapb.RecognitionConfig{
		Encoding:                   rivapb.AudioEncoding_LINEAR_PCM,
		SampleRateHertz:            int32(s.sampleRate),
		LanguageCode:               language,
		MaxAlternatives:            1,
		AudioChannelCount:          1,
		EnableWordTimeOffsets:      true,
		EnableAutomaticPunctuation: s.punctuate,
		Model:                      s.model,
	}
	if s.diarization {
		cfg.DiarizationConfig = &rivapb.SpeakerDiarizationConfig{
			EnableSpeakerDiarization: true,
			MaxSpeakerCount:          int32(s.maxSpeakerCount),
		}
	}
	return &rivapb.StreamingRecognitionConfig{
		Config:         cfg,
		InterimResults: true,
	}
}
