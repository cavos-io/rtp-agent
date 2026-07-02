package aws

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	bedrockruntime "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	awstypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/cavos-io/rtp-agent/core/audio/model"
)

func TestAWSRealtimeSessionStartsReferenceBedrockStream(t *testing.T) {
	stream := &fakeAWSRealtimeStream{}
	client := &fakeAWSRealtimeClient{stream: stream}
	provider := NewAWSRealtimeModel("amazon.nova-sonic-v1:0",
		WithAWSRealtimeClient(client),
		WithAWSRealtimeVoice("matthew"),
		WithAWSRealtimeTurnDetection("HIGH"),
	)

	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if client.input == nil || client.input.ModelId == nil {
		t.Fatalf("InvokeModelWithBidirectionalStream input = %#v, want model id", client.input)
	}
	if *client.input.ModelId != "amazon.nova-sonic-v1:0" {
		t.Fatalf("model id = %q, want configured Nova Sonic model", *client.input.ModelId)
	}
	if len(stream.sent) != 6 {
		t.Fatalf("sent init event count = %d, want 6", len(stream.sent))
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, stream.sent[0]), "event", "sessionStart", "endpointingSensitivity"); got != "HIGH" {
		t.Fatalf("endpointing sensitivity = %q, want HIGH", got)
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, stream.sent[1]), "event", "promptStart", "audioOutputConfiguration", "voiceId"); got != "matthew" {
		t.Fatalf("voiceId = %q, want matthew", got)
	}
	audioStart := mustAWSRealtimeJSONEvent(t, stream.sent[5])
	if got := awsRealtimeNestedString(audioStart, "event", "contentStart", "type"); got != "AUDIO" {
		t.Fatalf("event[5] type = %q, want AUDIO", got)
	}
	assertAWSRealtimeJSONNumber(t, nestedMap(t, audioStart, "event", "contentStart", "audioInputConfiguration")["sampleRateHertz"], 16000)
}

func TestAWSRealtimeSessionPushAudioAndCloseSendReferenceEvents(t *testing.T) {
	stream := &fakeAWSRealtimeStream{}
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	if err := session.PushAudio(&model.AudioFrame{Data: []byte{1, 2, 3}, SampleRate: 16000, NumChannels: 1}); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}
	audioInput := mustAWSRealtimeJSONEvent(t, stream.sent[len(stream.sent)-1])
	if got := awsRealtimeNestedString(audioInput, "event", "audioInput", "content"); got != base64.StdEncoding.EncodeToString([]byte{1, 2, 3}) {
		t.Fatalf("audioInput content = %q, want base64 PCM", got)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	closeEvents := stream.sent[len(stream.sent)-3:]
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, closeEvents[0]), "event", "contentEnd", "contentName"); got == "" {
		t.Fatalf("contentEnd contentName empty")
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, closeEvents[1]), "event", "promptEnd", "promptName"); got == "" {
		t.Fatalf("promptEnd promptName empty")
	}
	if _, ok := nestedMap(t, mustAWSRealtimeJSONEvent(t, closeEvents[2]), "event")["sessionEnd"].(map[string]any); !ok {
		t.Fatalf("sessionEnd event = %s", closeEvents[2])
	}
	if !stream.closed {
		t.Fatal("stream closed = false, want true")
	}
}

type fakeAWSRealtimeClient struct {
	input  *bedrockruntime.InvokeModelWithBidirectionalStreamInput
	stream awsRealtimeStream
	err    error
}

func (c *fakeAWSRealtimeClient) InvokeModelWithBidirectionalStream(ctx context.Context, input *bedrockruntime.InvokeModelWithBidirectionalStreamInput) (awsRealtimeStream, error) {
	c.input = input
	if c.err != nil {
		return nil, c.err
	}
	return c.stream, nil
}

type fakeAWSRealtimeStream struct {
	sent   []string
	closed bool
}

func (s *fakeAWSRealtimeStream) Send(_ context.Context, event awstypes.InvokeModelWithBidirectionalStreamInput) error {
	chunk, ok := event.(*awstypes.InvokeModelWithBidirectionalStreamInputMemberChunk)
	if !ok {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(chunk.Value.Bytes, &decoded); err == nil {
		encoded, _ := json.Marshal(decoded)
		s.sent = append(s.sent, string(encoded))
		return nil
	}
	s.sent = append(s.sent, string(chunk.Value.Bytes))
	return nil
}

func (s *fakeAWSRealtimeStream) Events() <-chan awstypes.InvokeModelWithBidirectionalStreamOutput {
	ch := make(chan awstypes.InvokeModelWithBidirectionalStreamOutput)
	close(ch)
	return ch
}

func (s *fakeAWSRealtimeStream) Close() error {
	s.closed = true
	return nil
}
