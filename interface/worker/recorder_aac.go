package worker

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

// writeMP4WithAAC encodes stereo 16-bit interleaved PCM to an AAC-in-MP4 file.
// Requires ffmpeg to be available on PATH.
// Left channel = user input, right channel = agent output (same layout as FLAC recording).
func writeMP4WithAAC(outputPath string, stereopcm []int16, sampleRate int) error {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("ffmpeg not found on PATH (required for AAC encoding): %w", err)
	}

	// Remove any stale file so FFmpeg never blocks on an overwrite prompt.
	os.Remove(outputPath)

	cmd := exec.Command(ffmpegPath,
		"-f", "s16le", // raw signed 16-bit little-endian PCM input
		"-ar", strconv.Itoa(sampleRate),
		"-ac", "2", // stereo
		"-i", "pipe:0", // read PCM from stdin
		"-c:a", "aac",
		"-b:a", "192k",
		"-movflags", "+faststart", // moov before mdat for browser streaming
		"-y", // never prompt
		outputPath,
	)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("opening ffmpeg stdin: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting ffmpeg: %w", err)
	}

	buf := make([]byte, len(stereopcm)*2)
	for i, s := range stereopcm {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}

	if _, err := stdin.Write(buf); err != nil {
		stdin.Close()
		_ = cmd.Wait()
		return fmt.Errorf("writing PCM to ffmpeg: %w", err)
	}
	stdin.Close()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg AAC encoding: %w", err)
	}
	return nil
}
