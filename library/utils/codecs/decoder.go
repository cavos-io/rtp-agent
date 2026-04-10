package codecs

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/hajimehoshi/go-mp3"
)

// AudioStreamDecoder is a generic interface for decoding compressed audio formats
// (like MP3, WAV, FLAC) into raw PCM AudioFrames suitable for LiveKit WebRTC output.
type AudioStreamDecoder interface {
	Push(data []byte)
	EndInput()
	Next() (*model.AudioFrame, error)
	Close() error
}

type DecoderType string

const (
	DecoderTypePCM DecoderType = "pcm"
	DecoderTypeMP3 DecoderType = "mp3"
)

type MP3AudioStreamDecoder struct {
	pipeReader *io.PipeReader
	pipeWriter *io.PipeWriter

	outputCh chan *model.AudioFrame
	errCh    chan error

	mu     sync.Mutex
	closed bool
	ended  bool
}

func NewMP3AudioStreamDecoder() AudioStreamDecoder {
	pr, pw := io.Pipe()
	d := &MP3AudioStreamDecoder{
		pipeReader: pr,
		pipeWriter: pw,
		outputCh:   make(chan *model.AudioFrame, 100),
		errCh:      make(chan error, 1),
	}

	go d.processLoop()
	return d
}

func (d *MP3AudioStreamDecoder) Push(data []byte) {
	d.mu.Lock()
	if d.closed || d.ended {
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()

	_, err := d.pipeWriter.Write(data)
	if err != nil {
		logger.Logger.Errorw("failed to write to mp3 decoder pipe", err)
	}
}

func (d *MP3AudioStreamDecoder) EndInput() {
	d.mu.Lock()
	if !d.ended {
		d.ended = true
		d.pipeWriter.Close()
	}
	d.mu.Unlock()
}

func (d *MP3AudioStreamDecoder) Next() (*model.AudioFrame, error) {
	select {
	case frame, ok := <-d.outputCh:
		if !ok {
			return nil, fmt.Errorf("decoder closed")
		}
		return frame, nil
	case err := <-d.errCh:
		return nil, err
	}
}

func (d *MP3AudioStreamDecoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.closed {
		d.closed = true
		d.pipeReader.Close()
		d.pipeWriter.Close()
	}
	return nil
}

func (d *MP3AudioStreamDecoder) processLoop() {
	defer close(d.outputCh)

	// go-mp3 requires a reader. It blocks until it reads the header.
	decoder, err := mp3.NewDecoder(d.pipeReader)
	if err != nil {
		d.errCh <- fmt.Errorf("failed to initialize mp3 decoder: %w", err)
		return
	}

	sampleRate := uint32(decoder.SampleRate())
	numChannels := uint32(2) // go-mp3 always outputs 16-bit stereo

	// Decode in chunks of 4096 bytes
	buf := make([]byte, 4096)
	for {
		n, err := decoder.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			d.outputCh <- &model.AudioFrame{
				Data:              chunk,
				SampleRate:        sampleRate,
				NumChannels:       numChannels,
				SamplesPerChannel: uint32(n) / (numChannels * 2), // 16-bit
			}
		}

		if err == io.EOF {
			break
		} else if err != nil {
			d.errCh <- fmt.Errorf("mp3 decode error: %w", err)
			break
		}
	}
}

// PCMAudioStreamDecoder implements AudioStreamDecoder for raw PCM streams
type PCMAudioStreamDecoder struct {
	sampleRate  int
	numChannels int
	
	buffer bytes.Buffer
	mu     sync.Mutex
	closed bool
	ended  bool
	
	outputCh chan *model.AudioFrame
	errCh    chan error
}

func NewPCMAudioStreamDecoder(sampleRate int, numChannels int) AudioStreamDecoder {
	d := &PCMAudioStreamDecoder{
		sampleRate:  sampleRate,
		numChannels: numChannels,
		outputCh:    make(chan *model.AudioFrame, 100),
		errCh:       make(chan error, 1),
	}
	
	go d.processLoop()
	return d
}

func (d *PCMAudioStreamDecoder) Push(data []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.ended {
		return
	}
	d.buffer.Write(data)
}

func (d *PCMAudioStreamDecoder) EndInput() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ended = true
}

func (d *PCMAudioStreamDecoder) Next() (*model.AudioFrame, error) {
	select {
	case frame, ok := <-d.outputCh:
		if !ok {
			return nil, fmt.Errorf("decoder closed")
		}
		return frame, nil
	case err := <-d.errCh:
		return nil, err
	}
}

func (d *PCMAudioStreamDecoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.closed {
		d.closed = true
		close(d.outputCh)
	}
	return nil
}

func (d *PCMAudioStreamDecoder) processLoop() {
	// Frame size calculation: e.g., 20ms at sampleRate * channels * 2 bytes/sample
	frameSize := (d.sampleRate * 20 / 1000) * d.numChannels * 2
	
	for {
		d.mu.Lock()
		if d.closed {
			d.mu.Unlock()
			return
		}
		
		if d.buffer.Len() >= frameSize {
			chunk := make([]byte, frameSize)
			d.buffer.Read(chunk)
			d.mu.Unlock()
			
			d.outputCh <- &model.AudioFrame{
				Data:              chunk,
				SampleRate:        uint32(d.sampleRate),
				NumChannels:       uint32(d.numChannels),
				SamplesPerChannel: uint32(frameSize / (d.numChannels * 2)),
			}
		} else if d.ended {
			// Flush remaining
			if d.buffer.Len() > 0 {
				chunk := d.buffer.Bytes()
				d.buffer.Reset()
				d.mu.Unlock()
				
				d.outputCh <- &model.AudioFrame{
					Data:              chunk,
					SampleRate:        uint32(d.sampleRate),
					NumChannels:       uint32(d.numChannels),
					SamplesPerChannel: uint32(len(chunk) / (d.numChannels * 2)),
				}
			} else {
				d.mu.Unlock()
			}
			return
		} else {
			d.mu.Unlock()
			time.Sleep(10 * time.Millisecond)
		}
	}
}
