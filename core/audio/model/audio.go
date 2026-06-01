package model

type AudioFrame struct {
	Data              []byte
	SampleRate        uint32
	NumChannels       uint32
	SamplesPerChannel uint32
}
