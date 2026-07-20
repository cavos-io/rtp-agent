package livekit

type recordingWriter interface {
	WritePCM(interleavedStereo []int16) (int, error)
	Close() error
}
