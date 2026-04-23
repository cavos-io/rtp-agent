package tokenize

import (
	"fmt"
	"strings"
	"sync"

	"github.com/cavos-io/rtp-agent/library/math"
)

type BufferedTokenStream struct {
	tokenizerFnc func(string) []string
	minTokenLen  int
	minCtxLen    int
	
	currentSegmentID string
	inBuf            string
	outBuf           string
	
	eventCh chan *TokenData
	closed  bool
	mu      sync.Mutex
}

func NewBufferedTokenStream(fnc func(string) []string, minTokenLen, minCtxLen int) *BufferedTokenStream {
	return &BufferedTokenStream{
		tokenizerFnc:     fnc,
		minTokenLen:      minTokenLen,
		minCtxLen:        minCtxLen,
		currentSegmentID: math.ShortUUID(""),
		eventCh:          make(chan *TokenData, 100),
	}
}

func (s *BufferedTokenStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("stream closed")
	}

	s.inBuf += text
	if len(s.inBuf) < s.minCtxLen {
		return nil
	}

	for {
		tokens := s.tokenizerFnc(s.inBuf)
		if len(tokens) <= 1 {
			break
		}

		if s.outBuf != "" {
			s.outBuf += " "
		}

		tok := tokens[0]
		tokens = tokens[1:]
		
		s.outBuf += tok
		if len(s.outBuf) >= s.minTokenLen {
			s.eventCh <- &TokenData{
				SegmentID: s.currentSegmentID,
				Token:     s.outBuf,
			}
			s.outBuf = ""
		}

		tokIdx := strings.Index(s.inBuf, tok)
		if tokIdx >= 0 {
			s.inBuf = strings.TrimLeft(s.inBuf[tokIdx+len(tok):], " ")
		} else {
			break
		}
	}
	return nil
}

func (s *BufferedTokenStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked()
}

func (s *BufferedTokenStream) flushLocked() error {
	if s.closed {
		return fmt.Errorf("stream closed")
	}

	if s.inBuf != "" || s.outBuf != "" {
		tokens := s.tokenizerFnc(s.inBuf)
		if len(tokens) > 0 {
			if s.outBuf != "" {
				s.outBuf += " "
			}
			s.outBuf += strings.Join(tokens, " ")
		}

		if s.outBuf != "" {
			s.eventCh <- &TokenData{
				SegmentID: s.currentSegmentID,
				Token:     s.outBuf,
			}
		}
	}

	s.currentSegmentID = math.ShortUUID("")
	s.inBuf = ""
	s.outBuf = ""
	return nil
}

func (s *BufferedTokenStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	
	// Ensure we flush on close if not already done manually
	_ = s.flushLocked()

	s.closed = true
	close(s.eventCh)
	return nil
}

func (s *BufferedTokenStream) Next() (*TokenData, error) {
	tok, ok := <-s.eventCh
	if !ok {
		return nil, fmt.Errorf("EOF")
	}
	return tok, nil
}

