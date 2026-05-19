package worker

import (
	"errors"
	"fmt"

	"github.com/asticode/go-astiav"
)

// astiavAAC is a per-instance AAC-LC encoder + MP4 muxer backed by go-astiav.
// Each RecorderIO gets its own instance, so concurrent recording sessions are
// fully independent — no global state, no shared mutex.
//
// Pipeline per WritePCM call:
//
//	[]int16 stereo S16
//	  → astiav.Frame (S16 interleaved)
//	  → SoftwareResampleContext → Frame (FLTP planar)   ← AAC encoder requires FLTP
//	  → AudioFifo (accumulate until ≥ frameSize samples)
//	  → CodecContext.SendFrame / ReceivePacket loop
//	  → FormatContext.WriteInterleavedFrame              ← MP4 muxer
type astiavAAC struct {
	formatCtx  *astiav.FormatContext
	ioCtx      *astiav.IOContext
	stream     *astiav.Stream
	codecCtx   *astiav.CodecContext
	swrCtx     *astiav.SoftwareResampleContext
	fifo       *astiav.AudioFifo
	pkt        *astiav.Packet
	inputFrame *astiav.Frame
	swrFrame   *astiav.Frame
	encFrame   *astiav.Frame
	pts        int64
	sampleRate int
}

func newAstiavAAC(outputPath string, sampleRate int) (*astiavAAC, error) {
	a := &astiavAAC{sampleRate: sampleRate}

	// --- codec context (AAC-LC encoder) ---
	codec := astiav.FindEncoder(astiav.CodecIDAac)
	if codec == nil {
		return nil, errors.New("astiavAAC: AAC encoder not found")
	}
	a.codecCtx = astiav.AllocCodecContext(codec)
	if a.codecCtx == nil {
		return nil, errors.New("astiavAAC: failed to alloc codec context")
	}
	a.codecCtx.SetSampleRate(sampleRate)
	a.codecCtx.SetChannelLayout(astiav.ChannelLayoutStereo)
	a.codecCtx.SetSampleFormat(astiav.SampleFormatFltp)
	a.codecCtx.SetBitRate(192000)
	if err := a.codecCtx.Open(codec, nil); err != nil {
		a.free()
		return nil, fmt.Errorf("astiavAAC: open codec context: %w", err)
	}

	// --- output format context (MP4) ---
	var err error
	a.formatCtx, err = astiav.AllocOutputFormatContext(nil, "mp4", outputPath)
	if err != nil {
		a.free()
		return nil, fmt.Errorf("astiavAAC: alloc output format context: %w", err)
	}

	// --- output stream ---
	a.stream = a.formatCtx.NewStream(codec)
	if a.stream == nil {
		a.free()
		return nil, errors.New("astiavAAC: failed to create output stream")
	}
	if err := a.codecCtx.ToCodecParameters(a.stream.CodecParameters()); err != nil {
		a.free()
		return nil, fmt.Errorf("astiavAAC: codec parameters: %w", err)
	}
	a.stream.SetTimeBase(a.codecCtx.TimeBase())

	// --- open file IO ---
	a.ioCtx, err = astiav.OpenIOContext(outputPath, astiav.NewIOContextFlags(astiav.IOContextFlagWrite), nil, nil)
	if err != nil {
		a.free()
		return nil, fmt.Errorf("astiavAAC: open io context: %w", err)
	}
	a.formatCtx.SetPb(a.ioCtx)

	if err := a.formatCtx.WriteHeader(nil); err != nil {
		a.free()
		return nil, fmt.Errorf("astiavAAC: write header: %w", err)
	}

	// --- software resampler: S16 interleaved → FLTP planar ---
	a.swrCtx = astiav.AllocSoftwareResampleContext()
	if a.swrCtx == nil {
		a.free()
		return nil, errors.New("astiavAAC: failed to alloc swr context")
	}

	// --- frames ---
	a.inputFrame = astiav.AllocFrame()
	a.swrFrame = astiav.AllocFrame()
	a.encFrame = astiav.AllocFrame()
	a.pkt = astiav.AllocPacket()
	if a.inputFrame == nil || a.swrFrame == nil || a.encFrame == nil || a.pkt == nil {
		a.free()
		return nil, errors.New("astiavAAC: failed to alloc frames/packet")
	}

	// Configure the encode frame (fixed frameSize samples, pre-allocated buffer).
	frameSize := a.codecCtx.FrameSize()
	a.encFrame.SetSampleFormat(astiav.SampleFormatFltp)
	a.encFrame.SetChannelLayout(astiav.ChannelLayoutStereo)
	a.encFrame.SetSampleRate(sampleRate)
	a.encFrame.SetNbSamples(frameSize)
	if err := a.encFrame.AllocBuffer(0); err != nil {
		a.free()
		return nil, fmt.Errorf("astiavAAC: alloc enc frame buffer: %w", err)
	}

	// --- audio fifo ---
	a.fifo = astiav.AllocAudioFifo(astiav.SampleFormatFltp, 2, frameSize)
	if a.fifo == nil {
		a.free()
		return nil, errors.New("astiavAAC: failed to alloc audio fifo")
	}

	return a, nil
}

// WritePCM encodes a batch of interleaved stereo int16 PCM samples into the MP4.
func (a *astiavAAC) WritePCM(stereo []int16) error {
	if len(stereo) == 0 {
		return nil
	}
	nbSamples := len(stereo) / 2

	// Fill input frame with S16 interleaved data.
	a.inputFrame.SetSampleFormat(astiav.SampleFormatS16)
	a.inputFrame.SetChannelLayout(astiav.ChannelLayoutStereo)
	a.inputFrame.SetSampleRate(a.sampleRate)
	a.inputFrame.SetNbSamples(nbSamples)
	if err := a.inputFrame.AllocBuffer(0); err != nil {
		return fmt.Errorf("astiavAAC: alloc input frame buffer: %w", err)
	}
	defer a.inputFrame.Unref()

	// Encode []int16 to S16 LE bytes and write directly into the frame buffer.
	// AllocBuffer(0) may allocate a buffer slightly larger than nbSamples*4 due to
	// ffmpeg's internal alignment padding. SetBytes requires the data to be exactly
	// the same size as the allocated buffer — so we pad with zeros to match.
	pcmBytes := make([]byte, len(stereo)*2)
	for i, s := range stereo {
		pcmBytes[i*2] = byte(s)
		pcmBytes[i*2+1] = byte(uint16(s) >> 8)
	}
	// Get the actual allocated buffer size and pad if necessary.
	if allocated, err2 := a.inputFrame.Data().Bytes(0); err2 == nil && len(allocated) > len(pcmBytes) {
		padded := make([]byte, len(allocated))
		copy(padded, pcmBytes)
		pcmBytes = padded
	}
	if err := a.inputFrame.Data().SetBytes(pcmBytes, 0); err != nil {
		return fmt.Errorf("astiavAAC: set input frame data: %w", err)
	}

	// Resample S16 → FLTP.
	// av_frame_unref resets all frame fields (format, sample_rate, ch_layout) to defaults,
	// so we must re-configure swrFrame before every ConvertFrame call.
	a.swrFrame.SetSampleFormat(astiav.SampleFormatFltp)
	a.swrFrame.SetChannelLayout(astiav.ChannelLayoutStereo)
	a.swrFrame.SetSampleRate(a.sampleRate)
	if err := a.swrCtx.ConvertFrame(a.inputFrame, a.swrFrame); err != nil {
		return fmt.Errorf("astiavAAC: resample: %w", err)
	}
	defer a.swrFrame.Unref()

	// Push resampled samples into the FIFO.
	if a.swrFrame.NbSamples() > 0 {
		if _, err := a.fifo.Write(a.swrFrame); err != nil {
			return fmt.Errorf("astiavAAC: fifo write: %w", err)
		}
	}

	// Flush any delayed samples still inside the resampler.
	for a.swrCtx.Delay(int64(a.sampleRate)) > 0 {
		a.swrFrame.SetSampleFormat(astiav.SampleFormatFltp)
		a.swrFrame.SetChannelLayout(astiav.ChannelLayoutStereo)
		a.swrFrame.SetSampleRate(a.sampleRate)
		if err := a.swrCtx.ConvertFrame(nil, a.swrFrame); err != nil {
			break
		}
		if a.swrFrame.NbSamples() == 0 {
			break
		}
		if _, err := a.fifo.Write(a.swrFrame); err != nil {
			return fmt.Errorf("astiavAAC: fifo write (flush): %w", err)
		}
		a.swrFrame.Unref()
	}

	return a.drainFIFO(false)
}

// drainFIFO reads complete AAC frames from the FIFO and encodes them.
// When flush is true, the remaining partial frame is also encoded (padded with silence).
func (a *astiavAAC) drainFIFO(flush bool) error {
	frameSize := a.codecCtx.FrameSize()
	for (flush && a.fifo.Size() > 0) || (!flush && a.fifo.Size() >= frameSize) {
		n, err := a.fifo.Read(a.encFrame)
		if err != nil {
			return fmt.Errorf("astiavAAC: fifo read: %w", err)
		}
		a.encFrame.SetNbSamples(n)
		a.encFrame.SetPts(a.pts)
		a.pts += int64(n)

		if err := a.sendAndReceive(a.encFrame); err != nil {
			return err
		}
	}
	return nil
}

// sendAndReceive sends one frame to the encoder and writes all resulting packets.
func (a *astiavAAC) sendAndReceive(f *astiav.Frame) error {
	if err := a.codecCtx.SendFrame(f); err != nil {
		return fmt.Errorf("astiavAAC: send frame: %w", err)
	}
	for {
		if err := a.codecCtx.ReceivePacket(a.pkt); err != nil {
			if errors.Is(err, astiav.ErrEagain) || errors.Is(err, astiav.ErrEof) {
				break
			}
			return fmt.Errorf("astiavAAC: receive packet: %w", err)
		}
		a.pkt.RescaleTs(a.codecCtx.TimeBase(), a.stream.TimeBase())
		a.pkt.SetStreamIndex(a.stream.Index())
		if err := a.formatCtx.WriteInterleavedFrame(a.pkt); err != nil {
			a.pkt.Unref()
			return fmt.Errorf("astiavAAC: write frame: %w", err)
		}
		a.pkt.Unref()
	}
	return nil
}

// Close flushes the encoder, writes the MP4 trailer, and releases all resources.
func (a *astiavAAC) Close() error {
	var firstErr error
	save := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// Flush FIFO remainder (partial frame, padded with silence by AllocBuffer).
	save(a.drainFIFO(true))

	// Flush encoder.
	save(a.sendAndReceive(nil))

	save(a.formatCtx.WriteTrailer())

	a.free()
	return firstErr
}

func (a *astiavAAC) free() {
	if a.fifo != nil {
		a.fifo.Free()
		a.fifo = nil
	}
	if a.pkt != nil {
		a.pkt.Free()
		a.pkt = nil
	}
	if a.encFrame != nil {
		a.encFrame.Free()
		a.encFrame = nil
	}
	if a.swrFrame != nil {
		a.swrFrame.Free()
		a.swrFrame = nil
	}
	if a.inputFrame != nil {
		a.inputFrame.Free()
		a.inputFrame = nil
	}
	if a.swrCtx != nil {
		a.swrCtx.Free()
		a.swrCtx = nil
	}
	if a.codecCtx != nil {
		a.codecCtx.Free()
		a.codecCtx = nil
	}
	if a.ioCtx != nil {
		_ = a.ioCtx.Close()
		a.ioCtx = nil
	}
	if a.formatCtx != nil {
		a.formatCtx.Free()
		a.formatCtx = nil
	}
}
