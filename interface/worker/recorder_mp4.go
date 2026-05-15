package worker

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/mewkiz/flac"
	flacframe "github.com/mewkiz/flac/frame"
	flacmeta "github.com/mewkiz/flac/meta"
)

// flacHeaderSize is the byte length of the FLAC file header written by the
// encoder: "fLaC" magic (4) + METADATA_BLOCK_HEADER (4) + STREAMINFO (34).
const flacHeaderSize = 42

// countWriter wraps an io.Writer and tracks total bytes written, used to
// measure individual FLAC frame sizes without buffering the entire output.
type countWriter struct {
	w io.Writer
	n int
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	return n, err
}

// encodeStereoFLAC encodes interleaved stereo 16-bit PCM to a FLAC stream.
// Returns per-frame byte sizes so the MP4 container can store each frame as a
// separate sample, enabling accurate currentTime reporting during browser playback.
func encodeStereoFLAC(w io.Writer, stereopcm []int16, sampleRate int) ([]int, error) {
	const channels = 2
	nSamples := len(stereopcm) / channels
	if nSamples == 0 {
		return nil, fmt.Errorf("no samples to encode")
	}

	const blockSize = 4096

	info := &flacmeta.StreamInfo{
		BlockSizeMin:  blockSize,
		BlockSizeMax:  blockSize,
		SampleRate:    uint32(sampleRate),
		NChannels:     channels,
		BitsPerSample: 16,
		NSamples:      uint64(nSamples),
	}

	cw := &countWriter{w: w}
	enc, err := flac.NewEncoder(cw, info)
	if err != nil {
		return nil, fmt.Errorf("creating FLAC encoder: %w", err)
	}

	var frameSizes []int
	var frameNum uint64
	for offset := 0; offset < nSamples; offset += blockSize {
		end := offset + blockSize
		if end > nSamples {
			end = nSamples
		}
		n := end - offset

		leftSamples := make([]int32, n)
		rightSamples := make([]int32, n)
		for i := 0; i < n; i++ {
			leftSamples[i] = int32(stereopcm[(offset+i)*2])
			rightSamples[i] = int32(stereopcm[(offset+i)*2+1])
		}

		f := &flacframe.Frame{
			Header: flacframe.Header{
				HasFixedBlockSize: true,
				BlockSize:         uint16(n),
				SampleRate:        uint32(sampleRate),
				Channels:          flacframe.ChannelsLR,
				BitsPerSample:     16,
				Num:               frameNum,
			},
			Subframes: []*flacframe.Subframe{
				{
					SubHeader: flacframe.SubHeader{Pred: flacframe.PredVerbatim},
					Samples:   leftSamples,
					NSamples:  n,
				},
				{
					SubHeader: flacframe.SubHeader{Pred: flacframe.PredVerbatim},
					Samples:   rightSamples,
					NSamples:  n,
				},
			},
		}

		before := cw.n
		if err := enc.WriteFrame(f); err != nil {
			return nil, fmt.Errorf("writing FLAC frame %d: %w", frameNum, err)
		}
		frameSizes = append(frameSizes, cw.n-before)
		frameNum++
	}

	return frameSizes, enc.Close()
}

// writeMP4WithFLAC writes a FLAC-in-MP4 (M4A) container file.
// Structure: ftyp → moov → mdat (faststart: moov before mdat so browsers can
// read duration and seek without downloading the full file first).
// Each FLAC audio frame is stored as a separate MP4 sample so browsers can
// update currentTime accurately during playback.
func writeMP4WithFLAC(outputPath string, flacData []byte, frameSizes []int, sampleRate int, totalSamples int64) error {
	if len(flacData) < flacHeaderSize {
		return fmt.Errorf("FLAC data too short (%d bytes)", len(flacData))
	}

	// Extract STREAMINFO metadata block from encoded FLAC file.
	// FLAC layout: "fLaC" (4) + METADATA_BLOCK_HEADER (4) + STREAMINFO (34) + ...
	streamInfoBlock := make([]byte, 38) // header(4) + STREAMINFO(34)
	copy(streamInfoBlock, flacData[4:42])
	streamInfoBlock[0] |= 0x80 // mark as last metadata block for dfLa box

	// mdat contains only the raw FLAC frames (no file header); STREAMINFO is
	// already embedded in the dfLa box inside moov.
	totalFrameBytes := 0
	for _, sz := range frameSizes {
		totalFrameBytes += sz
	}
	end := flacHeaderSize + totalFrameBytes
	if end > len(flacData) {
		end = len(flacData)
	}
	rawFrames := flacData[flacHeaderSize:end]

	ftyp := mp4BuildFtyp()

	// Two-pass moov build: first pass determines moov size, second pass sets the
	// correct mdat chunk offset (ftyp + moov + mdat_header).
	const mdatHeaderSize = 8
	dummyMoov := mp4BuildMoov(streamInfoBlock, sampleRate, totalSamples, frameSizes, 0)
	mdatContentOffset := int64(len(ftyp)) + int64(len(dummyMoov)) + mdatHeaderSize
	moov := mp4BuildMoov(streamInfoBlock, sampleRate, totalSamples, frameSizes, mdatContentOffset)

	mdat := mp4BuildBox("mdat", rawFrames)

	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("creating mp4 file: %w", err)
	}
	defer f.Close()

	for _, box := range [][]byte{ftyp, moov, mdat} {
		if _, err := f.Write(box); err != nil {
			return fmt.Errorf("writing mp4 box: %w", err)
		}
	}
	return nil
}

// --- MP4 box builders (ISO 14496-12, big-endian) ---

func mp4BuildBox(boxType string, content []byte) []byte {
	size := 8 + len(content)
	buf := make([]byte, size)
	binary.BigEndian.PutUint32(buf[0:4], uint32(size))
	copy(buf[4:8], boxType)
	copy(buf[8:], content)
	return buf
}

func mp4BuildFullBox(boxType string, version byte, flags uint32, content []byte) []byte {
	hdr := []byte{version, byte(flags >> 16), byte(flags >> 8), byte(flags)}
	return mp4BuildBox(boxType, append(hdr, content...))
}

func mp4u32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

func mp4u16(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}

func mp4IdentityMatrix() []byte {
	entries := []uint32{0x00010000, 0, 0, 0, 0x00010000, 0, 0, 0, 0x40000000}
	b := make([]byte, 36)
	for i, v := range entries {
		binary.BigEndian.PutUint32(b[i*4:], v)
	}
	return b
}

func mp4BuildFtyp() []byte {
	var c []byte
	c = append(c, "M4A "...)
	c = append(c, 0, 0, 0, 0)
	c = append(c, "M4A "...)
	c = append(c, "mp42"...)
	c = append(c, "isom"...)
	return mp4BuildBox("ftyp", c)
}

func mp4BuildMvhd(sampleRate int, totalSamples int64) []byte {
	var c []byte
	c = append(c, mp4u32(0)...)
	c = append(c, mp4u32(0)...)
	c = append(c, mp4u32(uint32(sampleRate))...)
	c = append(c, mp4u32(uint32(totalSamples))...)
	c = append(c, 0x00, 0x01, 0x00, 0x00) // rate = 1.0
	c = append(c, 0x01, 0x00)             // volume = 1.0
	c = append(c, make([]byte, 10)...)
	c = append(c, mp4IdentityMatrix()...)
	c = append(c, make([]byte, 24)...)
	c = append(c, mp4u32(2)...) // next_track_ID
	return mp4BuildFullBox("mvhd", 0, 0, c)
}

func mp4BuildTkhd(totalSamples int64, sampleRate int) []byte {
	var c []byte
	c = append(c, mp4u32(0)...)
	c = append(c, mp4u32(0)...)
	c = append(c, mp4u32(1)...)                    // track_ID
	c = append(c, mp4u32(0)...)                    // reserved
	c = append(c, mp4u32(uint32(totalSamples))...) // duration
	c = append(c, make([]byte, 8)...)
	c = append(c, 0, 0)       // layer
	c = append(c, 0, 0)       // alternate_group
	c = append(c, 0x01, 0x00) // volume = 1.0
	c = append(c, 0, 0)
	c = append(c, mp4IdentityMatrix()...)
	c = append(c, mp4u32(0)...) // width  (audio = 0)
	c = append(c, mp4u32(0)...) // height (audio = 0)
	return mp4BuildFullBox("tkhd", 0, 3, c) // flags=3: enabled + in_movie
}

func mp4BuildMdhd(sampleRate int, totalSamples int64) []byte {
	var c []byte
	c = append(c, mp4u32(0)...)
	c = append(c, mp4u32(0)...)
	c = append(c, mp4u32(uint32(sampleRate))...)
	c = append(c, mp4u32(uint32(totalSamples))...)
	c = append(c, 0x55, 0xC4) // language = 'und'
	c = append(c, 0, 0)
	return mp4BuildFullBox("mdhd", 0, 0, c)
}

func mp4BuildHdlr() []byte {
	var c []byte
	c = append(c, mp4u32(0)...)
	c = append(c, "soun"...)
	c = append(c, make([]byte, 12)...)
	c = append(c, "SoundHandler"...)
	c = append(c, 0)
	return mp4BuildFullBox("hdlr", 0, 0, c)
}

func mp4BuildSmhd() []byte {
	return mp4BuildFullBox("smhd", 0, 0, []byte{0, 0, 0, 0})
}

func mp4BuildDinf() []byte {
	urlBox := mp4BuildFullBox("url ", 0, 1, nil)
	dref := mp4BuildFullBox("dref", 0, 0, append(mp4u32(1), urlBox...))
	return mp4BuildBox("dinf", dref)
}

// mp4BuildDfLa builds the dfLa box containing FLAC StreamInfo for the sample entry.
// streamInfoBlock is 38 bytes: METADATA_BLOCK_HEADER(4) + STREAMINFO(34).
func mp4BuildDfLa(streamInfoBlock []byte) []byte {
	return mp4BuildFullBox("dfLa", 0, 0, streamInfoBlock)
}

func mp4BuildFLACSampleEntry(streamInfoBlock []byte, sampleRate int) []byte {
	var c []byte
	// SampleEntry base
	c = append(c, make([]byte, 6)...) // reserved[6]
	c = append(c, mp4u16(1)...)       // data_reference_index
	// AudioSampleEntry fields (ISO 14496-12 §12.2)
	c = append(c, make([]byte, 8)...)                    // reserved[2]
	c = append(c, mp4u16(2)...)                          // channelcount = 2 (stereo)
	c = append(c, mp4u16(16)...)                         // samplesize = 16-bit
	c = append(c, mp4u16(0)...)                          // pre_defined
	c = append(c, mp4u16(0)...)                          // reserved
	c = append(c, mp4u32(uint32(sampleRate)<<16)...)     // samplerate (16.16 fixed-point)
	c = append(c, mp4BuildDfLa(streamInfoBlock)...)
	return mp4BuildBox("fLaC", c)
}

// mp4BuildStts builds the time-to-sample (stts) box.
// All FLAC frames except possibly the last use blockSize audio samples; the
// last frame may have fewer. Consecutive equal-delta runs are grouped into a
// single stts entry to keep the box compact.
func mp4BuildStts(frameSizes []int, totalSamples int64) []byte {
	const blockSize = 4096
	numFrames := len(frameSizes)

	var entries []byte
	var entryCount uint32

	switch {
	case numFrames == 0:
		entryCount = 0
	case numFrames == 1:
		entryCount = 1
		entries = append(entries, mp4u32(1)...)
		entries = append(entries, mp4u32(uint32(totalSamples))...)
	default:
		lastFrameSamples := int(totalSamples) - (numFrames-1)*blockSize
		if lastFrameSamples == blockSize {
			// All frames have identical sample count.
			entryCount = 1
			entries = append(entries, mp4u32(uint32(numFrames))...)
			entries = append(entries, mp4u32(blockSize)...)
		} else {
			// Last frame is shorter.
			entryCount = 2
			entries = append(entries, mp4u32(uint32(numFrames-1))...)
			entries = append(entries, mp4u32(blockSize)...)
			entries = append(entries, mp4u32(1)...)
			entries = append(entries, mp4u32(uint32(lastFrameSamples))...)
		}
	}

	return mp4BuildFullBox("stts", 0, 0, append(mp4u32(entryCount), entries...))
}

// mp4BuildStsz builds the sample-size (stsz) box with a variable-size entry
// per FLAC frame.
func mp4BuildStsz(frameSizes []int) []byte {
	content := mp4u32(0) // sample_size=0 means variable; individual sizes follow
	content = append(content, mp4u32(uint32(len(frameSizes)))...)
	for _, sz := range frameSizes {
		content = append(content, mp4u32(uint32(sz))...)
	}
	return mp4BuildFullBox("stsz", 0, 0, content)
}

func mp4BuildStbl(streamInfoBlock []byte, sampleRate int, totalSamples int64, frameSizes []int, chunkOffset int64) []byte {
	stsdContent := append(mp4u32(1), mp4BuildFLACSampleEntry(streamInfoBlock, sampleRate)...)
	stsd := mp4BuildFullBox("stsd", 0, 0, stsdContent)

	stts := mp4BuildStts(frameSizes, totalSamples)

	// All frames belong to a single chunk.
	var stscEntry []byte
	stscEntry = append(stscEntry, mp4u32(1)...)                        // first_chunk
	stscEntry = append(stscEntry, mp4u32(uint32(len(frameSizes)))...)  // samples_per_chunk
	stscEntry = append(stscEntry, mp4u32(1)...)                        // sample_description_index
	stsc := mp4BuildFullBox("stsc", 0, 0, append(mp4u32(1), stscEntry...))

	stsz := mp4BuildStsz(frameSizes)

	stcoContent := append(mp4u32(1), mp4u32(uint32(chunkOffset))...)
	stco := mp4BuildFullBox("stco", 0, 0, stcoContent)

	var c []byte
	for _, box := range [][]byte{stsd, stts, stsc, stsz, stco} {
		c = append(c, box...)
	}
	return mp4BuildBox("stbl", c)
}

func mp4BuildMoov(streamInfoBlock []byte, sampleRate int, totalSamples int64, frameSizes []int, chunkOffset int64) []byte {
	smhd := mp4BuildSmhd()
	dinf := mp4BuildDinf()
	stbl := mp4BuildStbl(streamInfoBlock, sampleRate, totalSamples, frameSizes, chunkOffset)

	var minfContent []byte
	for _, b := range [][]byte{smhd, dinf, stbl} {
		minfContent = append(minfContent, b...)
	}
	minf := mp4BuildBox("minf", minfContent)

	mdhd := mp4BuildMdhd(sampleRate, totalSamples)
	hdlr := mp4BuildHdlr()
	var mdiaContent []byte
	for _, b := range [][]byte{mdhd, hdlr, minf} {
		mdiaContent = append(mdiaContent, b...)
	}
	mdia := mp4BuildBox("mdia", mdiaContent)

	tkhd := mp4BuildTkhd(totalSamples, sampleRate)
	trak := mp4BuildBox("trak", append(tkhd, mdia...))

	mvhd := mp4BuildMvhd(sampleRate, totalSamples)
	var moovContent []byte
	moovContent = append(moovContent, mvhd...)
	moovContent = append(moovContent, trak...)
	return mp4BuildBox("moov", moovContent)
}
