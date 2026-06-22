package clova

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/codecs"
	"github.com/cavos-io/rtp-agent/core/tts"
)

type ClovaTTS struct {
	clientID     string
	clientSecret string
	voice        string
}

func NewClovaTTS(clientID, clientSecret, voice string) *ClovaTTS {
	if voice == "" {
		voice = "nara"
	}
	return &ClovaTTS{
		clientID:     clientID,
		clientSecret: clientSecret,
		voice:        voice,
	}
}

func (t *ClovaTTS) Label() string { return "clova.TTS" }
func (t *ClovaTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *ClovaTTS) SampleRate() int  { return 24000 }
func (t *ClovaTTS) NumChannels() int { return 1 }

func (t *ClovaTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	apiURL := "https://naveropenapi.apigw.ntruss.com/tts-premium/v1/tts"

	data := url.Values{}
	data.Set("speaker", t.voice)
	data.Set("volume", "0")
	data.Set("speed", "0")
	data.Set("pitch", "0")
	data.Set("text", text)
	data.Set("format", "mp3")

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBufferString(data.Encode()))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-NCP-APIGW-API-KEY-ID", t.clientID)
	req.Header.Set("X-NCP-APIGW-API-KEY", t.clientSecret)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("clova tts error: %s", string(respBody))
	}

	return &clovaTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *ClovaTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming tts not natively supported by clova rest api")
}

type clovaTTSChunkedStream struct {
	resp    *http.Response
	decoder codecs.AudioStreamDecoder
	started bool
	final   bool
}

func (s *clovaTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.final {
		return nil, io.EOF
	}
	if !s.started {
		s.started = true
		s.decoder = codecs.NewMP3AudioStreamDecoder()
		data, err := io.ReadAll(s.resp.Body)
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			s.final = true
			return &tts.SynthesizedAudio{IsFinal: true}, nil
		}
		go func() {
			s.decoder.Push(data)
			s.decoder.EndInput()
		}()
	}

	frame, err := s.decoder.Next()
	if err != nil {
		if strings.Contains(err.Error(), "decoder closed") {
			if s.final {
				return nil, io.EOF
			}
			s.final = true
			return &tts.SynthesizedAudio{IsFinal: true}, nil
		}
		return nil, err
	}

	return &tts.SynthesizedAudio{
		Frame: frame,
	}, nil
}

func (s *clovaTTSChunkedStream) Close() error {
	s.final = true
	if s.decoder != nil {
		_ = s.decoder.Close()
	}
	return s.resp.Body.Close()
}
