//go:build vad

package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/signal"
	"time"

	"github.com/cavos-io/rtp-agent/adapter/silero"
	"github.com/cavos-io/rtp-agent/adapter/tenvad"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/model"
)

func main() {
	vadType := os.Getenv("VAD_TYPE")
	if vadType == "" {
		vadType = "silero"
	}

	fmt.Printf("--- VAD Standalone Test (No-Audio-Hardware Mode) ---\n")
	fmt.Printf("Testing VAD logic without requiring PortAudio/Opus headers.\n")
	fmt.Printf("Selected VAD: %s\n", vadType)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Setup VAD
	var selectedVAD vad.VAD
	switch vadType {
	case "silero":
		selectedVAD = silero.NewSileroVAD()
	case "tenvad":
		selectedVAD = tenvad.NewTenVAD()
	default:
		fmt.Printf("Invalid VAD_TYPE\n")
		return
	}

	stream, err := selectedVAD.Stream(ctx)
	if err != nil {
		fmt.Printf("Error creating VAD stream: %v\n", err)
		return
	}

	// 2. Generate/Simulate Audio Data (24kHz, 20ms frames)
	// We'll simulate 5 seconds of silence, 2 seconds of a sine wave (simulated speech), 3 seconds silence
	sampleRate := 24000
	frameSize := 480 // 20ms
	totalFrames := 500 // 10 seconds total

	fmt.Println("Starting simulation...")
	
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	go func() {
		fmt.Println("Pushing frames...")
		for i := 0; i < totalFrames; i++ {
			select {
			case <-ctx.Done():
				return
			default:
				// Create a frame
				pcm := make([]int16, frameSize)
				
				// Burst Noise Simulation (High Contrast)
				// Every 2 seconds (100 frames), send a 500ms burst of loud noise
				if (i / 100) % 2 == 1 {
					// LOUD Burst
					for j := 0; j < frameSize; j++ {
						// Extreme high frequency noise mixed with low
						noise := (math.Sin(float64(j)*0.5) * 0.4) + (math.Sin(float64(j)*2.0) * 0.4)
						pcm[j] = int16(noise * 31000) // Max intensity
					}
				}

				// Convert to bytes
				data := make([]byte, frameSize*2)
				for j, v := range pcm {
					data[j*2] = byte(v)
					data[j*2+1] = byte(v >> 8)
				}

				frame := &model.AudioFrame{
					Data:              data,
					SampleRate:        uint32(sampleRate),
					NumChannels:       1,
					SamplesPerChannel: uint32(frameSize),
				}

				if err := stream.PushFrame(frame); err != nil {
					fmt.Printf("\nPush error: %v\n", err)
					return
				}

				if i%50 == 0 {
					fmt.Printf(".") // Progress indicator
				}

				// Real-time simulation
				time.Sleep(10 * time.Millisecond) // speed up a bit for testing
			}
		}
		
		fmt.Println("\nFrames pushed. Waiting for processing to catch up...")
		time.Sleep(2 * time.Second) // Wait for VAD to finish processing
		fmt.Println("Simulation finished.")
		cancel()
	}()

	// 3. Read VAD Events
	go func() {
		for {
			event, err := stream.Next()
			if err != nil {
				return
			}

			switch event.Type {
			case vad.VADEventStartOfSpeech:
				fmt.Printf("\r [ SPEECH DETECTED ] %s", time.Now().Format("15:04:05.000"))
			case vad.VADEventEndOfSpeech:
				fmt.Printf("\r [ SILENCE DETECTED ] %s\n", time.Now().Format("15:04:05.000"))
			}
		}
	}()

	select {
	case <-sigCh:
		fmt.Println("\nStopped by user.")
	case <-ctx.Done():
	}
}
