package agent

import (
	"context"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/model"
)

type TranscriptionFilter struct {
	SpeakingRate float64
}

func NewTranscriptionFilter() *TranscriptionFilter {
	return &TranscriptionFilter{}
}

// TranscriptSynchronizer drip-feeds text to match the playout speed of audio.
type TranscriptSynchronizer struct {
	ctx      context.Context
	cancel   context.CancelFunc
	
	textCh   chan string
	audioCh  chan *model.AudioFrame
	eventCh  chan string
	
	mu             sync.Mutex
	textBuffer     string
	playedAudioDur time.Duration
	yieldedTextDur time.Duration
	speakingRate   float64 // syllables per second
	
	closed bool
}

// NewTranscriptSynchronizer initializes the synchronizer. Default speaking rate is usually ~3.83 syllables/sec.
func NewTranscriptSynchronizer(speakingRate float64) *TranscriptSynchronizer {
	if speakingRate <= 0 {
		speakingRate = 3.83
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &TranscriptSynchronizer{
		ctx:          ctx,
		cancel:       cancel,
		textCh:       make(chan string, 100),
		audioCh:      make(chan *model.AudioFrame, 100),
		eventCh:      make(chan string, 100),
		speakingRate: speakingRate,
	}

	go s.run()
	return s
}

func (s *TranscriptSynchronizer) PushText(text string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.textCh <- text
}

func (s *TranscriptSynchronizer) PushAudio(frame *model.AudioFrame) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.audioCh <- frame
}

func (s *TranscriptSynchronizer) EventCh() <-chan string {
	return s.eventCh
}

// Interrupt immediately flushes the remaining text buffer to the event channel and stops syncing.
func (s *TranscriptSynchronizer) Interrupt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if s.textBuffer != "" {
		s.eventCh <- s.textBuffer
		s.textBuffer = ""
	}
}

func (s *TranscriptSynchronizer) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	s.cancel()
	close(s.textCh)
	close(s.audioCh)
}

func countSyllables(text string) int {
	// A fast heuristic syllable counter using vowel groups
	text = strings.ToLower(text)
	text = regexp.MustCompile(`[^a-z]`).ReplaceAllString(text, "")
	
	if len(text) == 0 {
		return 0
	}
	if len(text) <= 3 {
		return 1
	}

	text = strings.TrimSuffix(text, "es")
	text = strings.TrimSuffix(text, "ed")
	text = strings.TrimSuffix(text, "e")
	
	vowels := regexp.MustCompile(`[aeiouy]+`)
	matches := vowels.FindAllStringIndex(text, -1)
	
	count := len(matches)
	if count == 0 {
		return 1
	}
	return count
}

func (s *TranscriptSynchronizer) run() {
	defer close(s.eventCh)
	
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			s.Interrupt()
			return
		
		case text, ok := <-s.textCh:
			if !ok {
				return
			}
			s.mu.Lock()
			s.textBuffer += text
			s.mu.Unlock()

		case frame, ok := <-s.audioCh:
			if !ok {
				return
			}
			if frame != nil && frame.SampleRate > 0 {
				dur := time.Duration(float64(frame.SamplesPerChannel)/float64(frame.SampleRate)*1000.0) * time.Millisecond
				s.mu.Lock()
				s.playedAudioDur += dur
				s.mu.Unlock()
			}
			
		case <-ticker.C:
			s.mu.Lock()
			if s.textBuffer == "" {
				s.mu.Unlock()
				continue
			}

			// How much audio time has passed that hasn't been covered by text yet?
			timeDebt := s.playedAudioDur - s.yieldedTextDur
			if timeDebt <= 0 {
				s.mu.Unlock()
				continue
			}

			// Calculate how many syllables we should emit for this time debt
			syllablesToEmit := int(math.Max(1.0, timeDebt.Seconds()*s.speakingRate))
			
			words := strings.Fields(s.textBuffer)
			var toEmit string
			var remaining string
			var emittedSyllables int
			
			for i, word := range words {
				syl := countSyllables(word)
				if emittedSyllables+syl > syllablesToEmit && toEmit != "" {
					remaining = strings.Join(words[i:], " ")
					// Preserve trailing spaces if the original had them
					if strings.HasSuffix(s.textBuffer, " ") {
						remaining += " "
					}
					break
				}
				toEmit += word + " "
				emittedSyllables += syl
			}

			if toEmit == "" {
				// Even the first word is too long, but we must emit something eventually
				// If debt is very high (e.g. 1 sec), force emit
				if timeDebt > time.Second {
					toEmit = words[0] + " "
					remaining = strings.Join(words[1:], " ")
					emittedSyllables = countSyllables(words[0])
				}
			}

			if toEmit != "" {
				// Calculate estimated duration of what we just emitted
				estDur := time.Duration(float64(emittedSyllables)/s.speakingRate*1000.0) * time.Millisecond
				s.yieldedTextDur += estDur
				s.textBuffer = remaining
				s.mu.Unlock()
				
				s.eventCh <- toEmit
			} else {
				s.mu.Unlock()
			}
		}
	}
}
