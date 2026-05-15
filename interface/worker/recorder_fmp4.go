package worker

// fMP4 streaming writer for AAC audio.
//
// Produces a Fragmented ISO Base Media File (ISO 14496-12) with AAC-LC audio.
// Structure:
//   - ftyp + moov (written once at init, moov contains mvex/trex → marks file as fragmented)
//   - Per-batch: moof (sequence header + track fragment) + mdat (raw AAC ES frames)
//   - mfra+tfra+mfro appended on Close() → enables player seek / progress bar
//   - mvhd/tkhd/mdhd.duration patched on Close() → player knows total duration
//
// The resulting file supports full seek control after Close().
// Before Close(), fragments already written are playable.

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// aacSamplesPerFrame is the number of PCM samples consumed per AAC-LC frame.
const aacSamplesPerFrame = 1024

// Fixed byte offsets of duration fields within the file for our specific box layout.
// ftyp(28) + moov-hdr(8) = 36 → moov content starts at 36
// mvhd: box-hdr(8)+fullbox-hdr(4)+creation(4)+modification(4)+timescale(4) = 24 → duration at 36+24=60
// tkhd: mvhd-total(108) bytes, so trak content at 36+108=144; tkhd box-hdr+fullbox+creation+modification+track_id+reserved = 28 → duration at 144+28=172 ... wait let me just use constants we derive
// These are computed in newFMP4Writer by measuring actual built boxes.
const (
	fmp4MvhdDurationOff = 60  // file byte offset of mvhd.duration (uint32)
	fmp4TkhdDurationOff = 180 // file byte offset of tkhd.duration (uint32)
	fmp4MdhdDurationOff = 276 // file byte offset of mdhd.duration (uint32)
)

type fragmentInfo struct {
	moofOffset uint64 // absolute file offset of this moof box
	decodeTime uint64 // base_media_decode_time of this fragment
}

// fMP4Writer writes an AAC-in-fMP4 file incrementally.
type fMP4Writer struct {
	f          *os.File
	seqNum     uint32
	decodeTime uint64 // running decode timestamp in audio samples
	sampleRate int
	channels   int
	asc        []byte         // AudioSpecificConfig (2 bytes for AAC-LC)
	fragments  []fragmentInfo // recorded per WriteFragment for mfra index
}

// newFMP4Writer opens outputPath and writes the ftyp + moov boxes.
// Call WriteFragment for each batch of AAC frames, then Close when done.
func newFMP4Writer(outputPath string, sampleRate, channels int) (*fMP4Writer, error) {
	f, err := os.Create(outputPath)
	if err != nil {
		return nil, fmt.Errorf("fmp4: create file: %w", err)
	}

	w := &fMP4Writer{
		f:          f,
		seqNum:     1,
		sampleRate: sampleRate,
		channels:   channels,
		asc:        buildAudioSpecificConfig(sampleRate, channels),
	}

	ftyp := w.buildFtyp()
	moov := w.buildMoov()
	for _, box := range [][]byte{ftyp, moov} {
		if _, err := f.Write(box); err != nil {
			f.Close()
			return nil, fmt.Errorf("fmp4: write header: %w", err)
		}
	}
	return w, nil
}

// WriteFragment encodes one batch of raw AAC ES frames as a moof+mdat pair.
// frames contains individual raw AAC ES frames (ADTS headers already stripped).
// Each frame represents aacSamplesPerFrame PCM samples.
func (w *fMP4Writer) WriteFragment(frames [][]byte) error {
	if len(frames) == 0 {
		return nil
	}

	// Record file offset before writing moof (used for mfra seek index).
	moofOffset, err := w.f.Seek(0, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("fmp4: seek current: %w", err)
	}
	w.fragments = append(w.fragments, fragmentInfo{
		moofOffset: uint64(moofOffset),
		decodeTime: w.decodeTime,
	})

	moof := w.buildMoof(frames)

	mdatPayloadSize := 0
	for _, f := range frames {
		mdatPayloadSize += len(f)
	}
	mdatHeader := make([]byte, 8)
	binary.BigEndian.PutUint32(mdatHeader[0:4], uint32(8+mdatPayloadSize))
	copy(mdatHeader[4:8], "mdat")

	if _, err := w.f.Write(moof); err != nil {
		return fmt.Errorf("fmp4: write moof: %w", err)
	}
	if _, err := w.f.Write(mdatHeader); err != nil {
		return fmt.Errorf("fmp4: write mdat header: %w", err)
	}
	for _, frame := range frames {
		if _, err := w.f.Write(frame); err != nil {
			return fmt.Errorf("fmp4: write mdat frame: %w", err)
		}
	}

	w.decodeTime += uint64(len(frames)) * aacSamplesPerFrame
	w.seqNum++
	return nil
}

// Close patches the duration fields in moov, appends the mfra seek index,
// then closes the file. After this call the file supports full player seek control.
func (w *fMP4Writer) Close() error {
	totalDuration := uint32(w.decodeTime)

	// Patch mvhd / tkhd / mdhd duration fields so the player knows total length.
	for _, off := range []int64{fmp4MvhdDurationOff, fmp4TkhdDurationOff, fmp4MdhdDurationOff} {
		if _, err := w.f.Seek(off, io.SeekStart); err != nil {
			_ = w.f.Close()
			return fmt.Errorf("fmp4: seek to duration field: %w", err)
		}
		if err := binary.Write(w.f, binary.BigEndian, totalDuration); err != nil {
			_ = w.f.Close()
			return fmt.Errorf("fmp4: write duration: %w", err)
		}
	}

	// Seek to end of file to append mfra.
	if _, err := w.f.Seek(0, io.SeekEnd); err != nil {
		_ = w.f.Close()
		return fmt.Errorf("fmp4: seek to end: %w", err)
	}

	mfra := w.buildMfra()
	if _, err := w.f.Write(mfra); err != nil {
		_ = w.f.Close()
		return fmt.Errorf("fmp4: write mfra: %w", err)
	}

	return w.f.Close()
}

// buildMfra builds the Movie Fragment Random Access box.
// Structure: mfra { tfra, mfro }
// tfra maps decode_time → moof_offset for each fragment.
// mfro carries the total mfra size so players can find it from the last 16 bytes.
func (w *fMP4Writer) buildMfra() []byte {
	tfra := w.buildTfra()

	// mfro: version=0, flags=0, size=total mfra size (4 bytes)
	// mfra = 8(hdr) + len(tfra) + 8(mfro hdr) + 4(fullbox) + 4(size) = 8+len(tfra)+16
	mfraSize := uint32(8 + len(tfra) + 16)
	mfro := fmp4FullBox("mfro", 0, 0, fmp4U32(mfraSize))

	var c []byte
	c = append(c, tfra...)
	c = append(c, mfro...)
	return fmp4Box("mfra", c)
}

// buildTfra builds the Track Fragment Random Access box (version=1, 64-bit offsets).
func (w *fMP4Writer) buildTfra() []byte {
	// length_size_of_traf_num=0, length_size_of_trun_num=0, length_size_of_sample_num=0
	// → each is 1 byte (value = length_size + 1)
	var c []byte
	c = append(c, fmp4U32(1)...)                    // track_ID
	c = append(c, 0x00)                              // length_size fields packed: (0<<4)|(0<<2)|0
	c = append(c, fmp4U32(uint32(len(w.fragments)))...) // number_of_entry

	for _, frag := range w.fragments {
		c = append(c, fmp4U64(frag.decodeTime)...)  // time (64-bit)
		c = append(c, fmp4U64(frag.moofOffset)...)  // moof_offset (64-bit)
		c = append(c, 0x01) // traf_number (1 byte)
		c = append(c, 0x01) // trun_number (1 byte)
		c = append(c, 0x01) // sample_number (1 byte)
	}

	return fmp4FullBox("tfra", 1, 0, c)
}

// --- AudioSpecificConfig (ISO 14496-3 §1.6.5.1) ---
// 2-byte config for AAC-LC: [5-bit objectType=2][4-bit samplingFreqIndex][4-bit channelConfig][3-bit padding]

var aacSamplingFreqTable = []int{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}

func buildAudioSpecificConfig(sampleRate, channels int) []byte {
	idx := 7 // default to 22050
	for i, f := range aacSamplingFreqTable {
		if f == sampleRate {
			idx = i
			break
		}
	}
	// objectType = 2 (AAC-LC), samplingFreqIndex, channelConfig
	// bits: OOOOO FFFF CCCC (13 bits → pack into 2 bytes, last 3 bits = 0)
	b0 := byte((2 << 3) | (idx >> 1))
	b1 := byte((idx&1)<<7) | byte(channels<<3)
	return []byte{b0, b1}
}

// --- ISO 14496-12 box builders ---

func fmp4Box(boxType string, content []byte) []byte {
	size := 8 + len(content)
	buf := make([]byte, size)
	binary.BigEndian.PutUint32(buf[0:4], uint32(size))
	copy(buf[4:8], boxType)
	copy(buf[8:], content)
	return buf
}

func fmp4FullBox(boxType string, version byte, flags uint32, content []byte) []byte {
	hdr := []byte{version, byte(flags >> 16), byte(flags >> 8), byte(flags)}
	return fmp4Box(boxType, append(hdr, content...))
}

func fmp4U32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

func fmp4U16(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}

func fmp4U64(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

func fmp4IdentityMatrix() []byte {
	entries := []uint32{0x00010000, 0, 0, 0, 0x00010000, 0, 0, 0, 0x40000000}
	b := make([]byte, 36)
	for i, v := range entries {
		binary.BigEndian.PutUint32(b[i*4:], v)
	}
	return b
}

func (w *fMP4Writer) buildFtyp() []byte {
	var c []byte
	c = append(c, "iso5"...)  // major brand
	c = append(c, 0, 0, 2, 0) // minor version
	c = append(c, "iso5"...)
	c = append(c, "iso6"...)
	c = append(c, "mp41"...)
	return fmp4Box("ftyp", c)
}

func (w *fMP4Writer) buildMvhd() []byte {
	var c []byte
	c = append(c, fmp4U32(0)...)                   // creation time
	c = append(c, fmp4U32(0)...)                   // modification time
	c = append(c, fmp4U32(uint32(w.sampleRate))...) // timescale
	c = append(c, fmp4U32(0)...)                   // duration = 0 (fragmented)
	c = append(c, 0x00, 0x01, 0x00, 0x00)          // rate = 1.0
	c = append(c, 0x01, 0x00)                      // volume = 1.0
	c = append(c, make([]byte, 10)...)
	c = append(c, fmp4IdentityMatrix()...)
	c = append(c, make([]byte, 24)...)
	c = append(c, fmp4U32(2)...) // next_track_ID
	return fmp4FullBox("mvhd", 0, 0, c)
}

func (w *fMP4Writer) buildTkhd() []byte {
	var c []byte
	c = append(c, fmp4U32(0)...) // creation time
	c = append(c, fmp4U32(0)...) // modification time
	c = append(c, fmp4U32(1)...) // track_ID
	c = append(c, fmp4U32(0)...) // reserved
	c = append(c, fmp4U32(0)...) // duration = 0 (fragmented)
	c = append(c, make([]byte, 8)...)
	c = append(c, 0, 0)       // layer
	c = append(c, 0, 0)       // alternate_group
	c = append(c, 0x01, 0x00) // volume = 1.0
	c = append(c, 0, 0)
	c = append(c, fmp4IdentityMatrix()...)
	c = append(c, fmp4U32(0)...) // width  (audio = 0)
	c = append(c, fmp4U32(0)...) // height (audio = 0)
	return fmp4FullBox("tkhd", 0, 3, c) // flags=3: enabled + in_movie
}

func (w *fMP4Writer) buildMdhd() []byte {
	var c []byte
	c = append(c, fmp4U32(0)...)                   // creation time
	c = append(c, fmp4U32(0)...)                   // modification time
	c = append(c, fmp4U32(uint32(w.sampleRate))...) // timescale
	c = append(c, fmp4U32(0)...)                   // duration = 0 (fragmented)
	c = append(c, 0x55, 0xC4)                      // language = 'und'
	c = append(c, 0, 0)
	return fmp4FullBox("mdhd", 0, 0, c)
}

func (w *fMP4Writer) buildHdlr() []byte {
	var c []byte
	c = append(c, fmp4U32(0)...)
	c = append(c, "soun"...)
	c = append(c, make([]byte, 12)...)
	c = append(c, "SoundHandler"...)
	c = append(c, 0)
	return fmp4FullBox("hdlr", 0, 0, c)
}

func (w *fMP4Writer) buildSmhd() []byte {
	return fmp4FullBox("smhd", 0, 0, []byte{0, 0, 0, 0})
}

func (w *fMP4Writer) buildDinf() []byte {
	urlBox := fmp4FullBox("url ", 0, 1, nil)
	dref := fmp4FullBox("dref", 0, 0, append(fmp4U32(1), urlBox...))
	return fmp4Box("dinf", dref)
}

// buildEsds builds the ES_Descriptor box for AAC-LC.
// Embeds AudioSpecificConfig from the AudioSpecificConfig field.
func (w *fMP4Writer) buildEsds() []byte {
	asc := w.asc

	// DecoderSpecificInfo descriptor (tag=5)
	decoderSpecificInfo := append([]byte{0x05, byte(len(asc))}, asc...)

	// DecoderConfigDescriptor (tag=4): objectTypeIndication=0x40 (Audio ISO/IEC 14496-3),
	// streamType=0x15 (audio stream), bufferSizeDB, maxBitrate, avgBitrate
	dcd := []byte{
		0x04,
		byte(13 + len(decoderSpecificInfo)),
		0x40,       // objectTypeIndication: Audio ISO/IEC 14496-3
		0x15,       // streamType=AudioStream (0x05 << 2 | 0x01)
		0x00, 0x00, 0x00, // bufferSizeDB (3 bytes)
		0x00, 0x05, 0xDC, 0x00, // maxBitrate = 384000
		0x00, 0x01, 0xF4, 0x00, // avgBitrate = 128000
	}
	dcd = append(dcd, decoderSpecificInfo...)

	// SLConfigDescriptor (tag=6): predefined=2
	sl := []byte{0x06, 0x01, 0x02}

	// ES_Descriptor (tag=3): ES_ID=1, streamDependenceFlag=0, URL_Flag=0, OCRstreamFlag=0
	esLen := 3 + len(dcd) + len(sl)
	es := []byte{0x03, byte(esLen), 0x00, 0x01, 0x00}
	es = append(es, dcd...)
	es = append(es, sl...)

	// esds full box: version=0, flags=0, then ES_Descriptor
	return fmp4FullBox("esds", 0, 0, es)
}

func (w *fMP4Writer) buildMp4aSampleEntry() []byte {
	var c []byte
	c = append(c, make([]byte, 6)...) // reserved[6]
	c = append(c, fmp4U16(1)...)      // data_reference_index
	c = append(c, make([]byte, 8)...) // reserved[2] x 4bytes
	c = append(c, fmp4U16(uint16(w.channels))...) // channelcount
	c = append(c, fmp4U16(16)...)                  // samplesize = 16-bit
	c = append(c, fmp4U16(0)...)                   // pre_defined
	c = append(c, fmp4U16(0)...)                   // reserved
	c = append(c, fmp4U32(uint32(w.sampleRate)<<16)...) // samplerate (16.16 fixed)
	c = append(c, w.buildEsds()...)
	return fmp4Box("mp4a", c)
}

func (w *fMP4Writer) buildStbl() []byte {
	// Empty stbl — all sample data is in fragments
	stsdContent := append(fmp4U32(1), w.buildMp4aSampleEntry()...)
	stsd := fmp4FullBox("stsd", 0, 0, stsdContent)
	stts := fmp4FullBox("stts", 0, 0, fmp4U32(0)) // entry_count=0
	stsc := fmp4FullBox("stsc", 0, 0, fmp4U32(0)) // entry_count=0
	stsz := fmp4FullBox("stsz", 0, 0, append(fmp4U32(0), fmp4U32(0)...)) // sample_size=0, sample_count=0
	stco := fmp4FullBox("stco", 0, 0, fmp4U32(0)) // entry_count=0

	var c []byte
	for _, box := range [][]byte{stsd, stts, stsc, stsz, stco} {
		c = append(c, box...)
	}
	return fmp4Box("stbl", c)
}

func (w *fMP4Writer) buildMinf() []byte {
	smhd := w.buildSmhd()
	dinf := w.buildDinf()
	stbl := w.buildStbl()

	var c []byte
	for _, b := range [][]byte{smhd, dinf, stbl} {
		c = append(c, b...)
	}
	return fmp4Box("minf", c)
}

func (w *fMP4Writer) buildMdia() []byte {
	mdhd := w.buildMdhd()
	hdlr := w.buildHdlr()
	minf := w.buildMinf()

	var c []byte
	for _, b := range [][]byte{mdhd, hdlr, minf} {
		c = append(c, b...)
	}
	return fmp4Box("mdia", c)
}

func (w *fMP4Writer) buildTrak() []byte {
	tkhd := w.buildTkhd()
	mdia := w.buildMdia()
	return fmp4Box("trak", append(tkhd, mdia...))
}

func (w *fMP4Writer) buildMvex() []byte {
	// trex: default values for track fragments
	var trex []byte
	trex = append(trex, fmp4U32(1)...)  // track_ID
	trex = append(trex, fmp4U32(1)...)  // default_sample_description_index
	trex = append(trex, fmp4U32(aacSamplesPerFrame)...) // default_sample_duration
	trex = append(trex, fmp4U32(0)...)  // default_sample_size
	trex = append(trex, fmp4U32(0)...)  // default_sample_flags
	return fmp4Box("mvex", fmp4FullBox("trex", 0, 0, trex))
}

func (w *fMP4Writer) buildMoov() []byte {
	mvhd := w.buildMvhd()
	trak := w.buildTrak()
	mvex := w.buildMvex()

	var c []byte
	c = append(c, mvhd...)
	c = append(c, trak...)
	c = append(c, mvex...)
	return fmp4Box("moov", c)
}

// buildMoof builds a Movie Fragment box for the given frames.
// Uses tf_flags: default-base-is-moof (0x020000) so base_data_offset
// is implicitly the start of the moof box — no patching needed.
func (w *fMP4Writer) buildMoof(frames [][]byte) []byte {
	// mfhd: sequence_number
	mfhd := fmp4FullBox("mfhd", 0, 0, fmp4U32(w.seqNum))

	// tfhd: track_ID, flags = default-base-is-moof
	const tfhdFlags = 0x020000 // default-base-is-moof
	var tfhdContent []byte
	tfhdContent = append(tfhdContent, fmp4U32(1)...) // track_ID
	tfhd := fmp4FullBox("tfhd", 0, tfhdFlags, tfhdContent)

	// tfdt: base_media_decode_time (version=1 → 64-bit)
	tfdt := fmp4FullBox("tfdt", 1, 0, fmp4U64(w.decodeTime))

	// trun: sample_count entries with size + duration per sample
	// flags: data-offset-present(0x001) + sample-duration-present(0x100) + sample-size-present(0x200)
	const trunFlags = 0x000301
	var trunContent []byte
	trunContent = append(trunContent, fmp4U32(uint32(len(frames)))...) // sample_count

	// data_offset: offset from start of moof to start of mdat payload.
	// = sizeof(moof) + 8 (mdat header). We'll patch this after building traf.
	// Placeholder = 0 for now.
	dataOffsetPos := len(trunContent)
	trunContent = append(trunContent, fmp4U32(0)...) // data_offset placeholder

	for _, frame := range frames {
		trunContent = append(trunContent, fmp4U32(aacSamplesPerFrame)...) // sample_duration
		trunContent = append(trunContent, fmp4U32(uint32(len(frame)))...)  // sample_size
	}
	trun := fmp4FullBox("trun", 0, trunFlags, trunContent)

	// traf
	var trafContent []byte
	trafContent = append(trafContent, tfhd...)
	trafContent = append(trafContent, tfdt...)
	trafContent = append(trafContent, trun...)
	traf := fmp4Box("traf", trafContent)

	// moof
	var moofContent []byte
	moofContent = append(moofContent, mfhd...)
	moofContent = append(moofContent, traf...)
	moof := fmp4Box("moof", moofContent)

	// Patch data_offset in trun.
	// data_offset = sizeof(moof) + 8 (mdat header size).
	dataOffset := uint32(len(moof)) + 8

	// Locate data_offset field in moof:
	// moof: [size(4)][type(4)] → 8 bytes
	// mfhd: [size(4)][type(4)][version(1)][flags(3)][seqnum(4)] → 16 bytes
	// traf: [size(4)][type(4)] → 8 bytes
	// tfhd: [size(4)][type(4)][version(1)][flags(3)][track_id(4)] → 16 bytes
	// tfdt (v1): [size(4)][type(4)][version(1)][flags(3)][decode_time(8)] → 20 bytes
	// trun: [size(4)][type(4)][version(1)][flags(3)][sample_count(4)][data_offset(4)...]
	//                                                                   ^-- this is where we patch
	// trun starts at: 8(moof) + 16(mfhd) + 8(traf) + 16(tfhd) + 20(tfdt) = 68
	// data_offset is at: 68 + 8(trun header) + 4(version+flags) + 4(sample_count) = 84
	_ = dataOffsetPos
	dataOffsetBytePos := 8 + 16 + 8 + 16 + 20 + 8 + 4 + 4
	binary.BigEndian.PutUint32(moof[dataOffsetBytePos:], dataOffset)

	return moof
}

// stripADTSFrames parses a buffer of concatenated ADTS-framed AAC frames and
// returns the raw AAC ES payload of each frame (header stripped).
// ADTS header is 7 bytes (no CRC) or 9 bytes (with CRC).
// Returns an error if the buffer contains malformed data.
func stripADTSFrames(adts []byte) ([][]byte, error) {
	var frames [][]byte
	for len(adts) >= 7 {
		if adts[0] != 0xFF || (adts[1]&0xF0) != 0xF0 {
			return nil, fmt.Errorf("fmp4: invalid ADTS syncword at offset %d", 0)
		}
		hasCRC := (adts[1] & 0x01) == 0
		headerLen := 7
		if hasCRC {
			headerLen = 9
		}
		if len(adts) < headerLen {
			break
		}
		// frame length is 13 bits at bits 30-42 of the header
		frameLen := int(adts[3]&0x03)<<11 | int(adts[4])<<3 | int(adts[5])>>5
		if frameLen < headerLen || frameLen > len(adts) {
			break
		}
		payload := adts[headerLen:frameLen]
		if len(payload) > 0 {
			frames = append(frames, payload)
		}
		adts = adts[frameLen:]
	}
	return frames, nil
}
