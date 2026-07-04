package aws

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	awstypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type awsRequestTestTool struct{}

func (awsRequestTestTool) ID() string          { return "lookup" }
func (awsRequestTestTool) Name() string        { return "lookup" }
func (awsRequestTestTool) Description() string { return "look up information" }
func (awsRequestTestTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
		"required": []string{"query"},
	}
}
func (awsRequestTestTool) Execute(context.Context, string) (string, error) {
	return "", nil
}

type awsEmptyDescriptionTool struct {
	awsRequestTestTool
}

func (awsEmptyDescriptionTool) Description() string { return "" }

func TestAWSLLMDefaultsMatchReference(t *testing.T) {
	provider := &AWSLLM{model: defaultAWSLLMModel}

	if got := awsLLMModelOrDefault(""); got != "amazon.nova-2-lite-v1:0" {
		t.Fatalf("default model = %q, want reference default model", got)
	}
	if got := awsLLMModelOrDefault("custom-model"); got != "custom-model" {
		t.Fatalf("explicit model = %q, want custom-model", got)
	}
	if provider.Label() != "aws.LLM" {
		t.Fatalf("Label = %q, want aws.LLM", provider.Label())
	}
	if provider.Model() != "amazon.nova-2-lite-v1:0" {
		t.Fatalf("Model = %q, want reference default model", provider.Model())
	}
	if provider.Provider() != "AWS Bedrock" {
		t.Fatalf("Provider = %q, want AWS Bedrock", provider.Provider())
	}
}

func TestNewAWSLLMUsesReferenceDefaults(t *testing.T) {
	provider, err := NewAWSLLM(context.Background(), "", "")
	if err != nil {
		t.Fatalf("NewAWSLLM error = %v, want nil with default region/model", err)
	}
	if provider.Model() != "amazon.nova-2-lite-v1:0" {
		t.Fatalf("Model = %q, want reference default model", provider.Model())
	}
}

func TestAWSLLMExplicitCredentialsMatchReference(t *testing.T) {
	creds := AWSCredentials{
		AccessKeyID:     "test-access",
		SecretAccessKey: "test-secret",
		SessionToken:    "test-token",
	}
	provider, err := NewAWSLLM(context.Background(), "us-west-2", "", WithAWSLLMCredentials(creds))
	if err != nil {
		t.Fatalf("NewAWSLLM error = %v, want nil with explicit credentials", err)
	}
	if !provider.credentialsSet {
		t.Fatal("credentialsSet = false, want explicit credentials stored")
	}
	if provider.credentials != creds {
		t.Fatalf("credentials = %#v, want %#v", provider.credentials, creds)
	}
}

func TestAWSRegionDefaultMatchesReference(t *testing.T) {
	if got := awsRegionOrDefault(""); got != "us-east-1" {
		t.Fatalf("default region = %q, want us-east-1", got)
	}
	if got := awsRegionOrDefault("ap-southeast-1"); got != "ap-southeast-1" {
		t.Fatalf("explicit region = %q, want ap-southeast-1", got)
	}
}

func TestAWSLLMChatRequiresConfiguredClient(t *testing.T) {
	provider := &AWSLLM{model: defaultAWSLLMModel}
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	_, err := provider.Chat(context.Background(), ctx)

	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Chat error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(err.Error(), "client is not configured") {
		t.Fatalf("Chat error = %v, want configured-client context", err)
	}
}

func TestAWSLLMChatReturnsAPIConnectionErrorOnTransportError(t *testing.T) {
	provider := &AWSLLM{
		client: fakeAWSLLMClient{err: errors.New("bedrock dial failed")},
		model:  defaultAWSLLMModel,
	}
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	stream, err := provider.Chat(context.Background(), ctx)

	if err != nil {
		t.Fatalf("Chat error = %v, want lazy stream", err)
	}
	chunk, err := stream.Next()
	if chunk != nil {
		t.Fatalf("Next chunk = %#v, want nil on startup transport error", chunk)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(err.Error(), "AWS Bedrock LLM chat failed") {
		t.Fatalf("Next error = %q, want Bedrock chat context", err.Error())
	}
}

func TestAWSLLMChatWrapsReferenceProviderStatusAsConnectionError(t *testing.T) {
	header := http.Header{}
	header.Set("x-amzn-requestid", "aws-request-429")
	providerErr := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     header,
		}},
		Err: errors.New("throttled"),
	}
	provider := &AWSLLM{
		client: fakeAWSLLMClient{err: providerErr},
		model:  defaultAWSLLMModel,
	}
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	stream, err := provider.Chat(context.Background(), ctx)

	if err != nil {
		t.Fatalf("Chat error = %v, want lazy stream", err)
	}
	chunk, err := stream.Next()
	if chunk != nil {
		t.Fatalf("Next chunk = %#v, want nil on startup status error", chunk)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !connectionErr.Retryable {
		t.Fatal("Retryable = false, want true before any chunk")
	}
	if !strings.Contains(connectionErr.Error(), "aws bedrock llm: error generating content") {
		t.Fatalf("Next error = %q, want reference Bedrock content context", connectionErr.Error())
	}
	if !strings.Contains(connectionErr.Error(), "throttled") {
		t.Fatalf("Next error = %q, want provider failure detail", connectionErr.Error())
	}
	var statusErr *llm.APIStatusError
	if errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T, want provider status wrapped as APIConnectionError", err)
	}
}

func TestAWSLLMChatReturnsAPITimeoutErrorOnDeadline(t *testing.T) {
	provider := &AWSLLM{
		client: fakeAWSLLMClient{err: context.DeadlineExceeded},
		model:  defaultAWSLLMModel,
	}
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	stream, err := provider.Chat(context.Background(), ctx)

	if err != nil {
		t.Fatalf("Chat error = %v, want lazy stream", err)
	}
	chunk, err := stream.Next()
	if chunk != nil {
		t.Fatalf("Next chunk = %#v, want nil on startup timeout", chunk)
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next error = %T %v, want APITimeoutError", err, err)
	}
}

func TestAWSLLMChatCallerCancelReturnsContextCanceled(t *testing.T) {
	provider := &AWSLLM{
		client: fakeAWSLLMClient{err: context.Canceled},
		model:  defaultAWSLLMModel,
	}
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	stream, err := provider.Chat(context.Background(), ctx)

	if err != nil {
		t.Fatalf("Chat error = %v, want lazy stream", err)
	}
	chunk, err := stream.Next()
	if chunk != nil {
		t.Fatalf("Next chunk = %#v, want nil on caller cancellation", chunk)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Next error = %T %v, want context.Canceled", err, err)
	}
}

func TestAWSLLMChatAppliesConnectOptionsTimeoutToRequestContext(t *testing.T) {
	var captured context.Context
	provider := &AWSLLM{
		client: fakeAWSLLMClient{
			err:        errors.New("stop after capture"),
			ctxCapture: &captured,
		},
		model: defaultAWSLLMModel,
	}
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	stream, err := provider.Chat(context.Background(), ctx, llm.WithConnectOptions(llm.APIConnectOptions{Timeout: 75 * time.Millisecond}))
	if err != nil {
		t.Fatalf("Chat error = %v, want lazy stream", err)
	}
	_, _ = stream.Next()

	deadline, ok := captured.Deadline()
	if !ok {
		t.Fatal("request context has no deadline, want connect options timeout")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > 75*time.Millisecond {
		t.Fatalf("request deadline remaining = %v, want within configured timeout", remaining)
	}
}

func TestAWSLLMChatReturnsBeforeReferenceProviderStreamStarts(t *testing.T) {
	client := &blockingAWSLLMClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	provider := &AWSLLM{
		client: client,
		model:  defaultAWSLLMModel,
	}
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	resultCh := make(chan llm.LLMStream, 1)
	errCh := make(chan error, 1)
	go func() {
		stream, err := provider.Chat(context.Background(), ctx)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- stream
	}()

	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("provider stream did not start")
	}

	select {
	case stream := <-resultCh:
		_ = stream.Close()
	case err := <-errCh:
		t.Fatalf("Chat error = %v, want stream returned while provider start is pending", err)
	case <-time.After(150 * time.Millisecond):
		close(client.release)
		t.Fatal("Chat blocked behind provider stream start; want reference background startup")
	}

	close(client.release)
}

func TestAWSLLMStreamCloseUnblocksPendingReferenceStartupNext(t *testing.T) {
	client := &blockingAWSLLMClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	provider := &AWSLLM{
		client: client,
		model:  defaultAWSLLMModel,
	}
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	stream, err := provider.Chat(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Chat error = %v", err)
	}
	defer close(client.release)

	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("provider stream did not start")
	}

	nextDone := make(chan error, 1)
	go func() {
		chunk, err := stream.Next()
		if chunk != nil {
			nextDone <- fmt.Errorf("Next chunk = %#v, want nil after Close", chunk)
			return
		}
		nextDone <- err
	}()

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	select {
	case err := <-nextDone:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Next error = %v, want EOF after Close", err)
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatal("Next remained blocked behind provider startup after Close")
	}
}

func TestAWSLLMStreamCloseClosesLateReferenceProviderStartup(t *testing.T) {
	reader := newFakeAWSLLMReader()
	client := &lateAWSLLMClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
		out:     newFakeAWSLLMOutput(reader),
	}
	provider := &AWSLLM{
		client: client,
		model:  defaultAWSLLMModel,
	}
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	stream, err := provider.Chat(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Chat error = %v", err)
	}
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("provider stream did not start")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	close(client.release)

	deadline := time.After(time.Second)
	for !reader.closed {
		select {
		case <-deadline:
			t.Fatal("late provider stream closed = false, want Close to release Bedrock stream returned after cancellation")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestAWSLLMChatAppliesReferenceInferenceConfig(t *testing.T) {
	provider := &AWSLLM{
		client: fakeAWSLLMClient{err: errors.New("stop after capture")},
		model:  defaultAWSLLMModel,
	}
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	stream, err := provider.Chat(context.Background(), ctx, llm.WithExtraParams(map[string]any{
		"max_output_tokens": 128,
		"temperature":       0.2,
		"top_p":             0.7,
	}))
	if err != nil {
		t.Fatalf("Chat error = %v", err)
	}

	input := stream.(*awsLLMStream).request
	if input == nil || input.InferenceConfig == nil {
		t.Fatalf("InferenceConfig = %#v, want reference Bedrock inference config", input)
	}
	if input.InferenceConfig.MaxTokens == nil || *input.InferenceConfig.MaxTokens != 128 {
		t.Fatalf("max tokens = %#v, want 128", input.InferenceConfig.MaxTokens)
	}
	if input.InferenceConfig.Temperature == nil || *input.InferenceConfig.Temperature != 0.2 {
		t.Fatalf("temperature = %#v, want 0.2", input.InferenceConfig.Temperature)
	}
	if input.InferenceConfig.TopP == nil || *input.InferenceConfig.TopP != 0.7 {
		t.Fatalf("topP = %#v, want 0.7", input.InferenceConfig.TopP)
	}
}

func TestAWSLLMChatAppliesReferenceProviderInferenceDefaults(t *testing.T) {
	provider := &AWSLLM{
		client: fakeAWSLLMClient{err: errors.New("stop after capture")},
		model:  defaultAWSLLMModel,
	}
	WithAWSLLMMaxOutputTokens(256)(provider)
	WithAWSLLMTemperature(0.3)(provider)
	WithAWSLLMTopP(0.8)(provider)
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	stream, err := provider.Chat(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Chat error = %v", err)
	}
	input := stream.(*awsLLMStream).request

	if input == nil || input.InferenceConfig == nil {
		t.Fatalf("InferenceConfig = %#v, want provider defaults", input)
	}
	if input.InferenceConfig.MaxTokens == nil || *input.InferenceConfig.MaxTokens != 256 {
		t.Fatalf("max tokens = %#v, want 256", input.InferenceConfig.MaxTokens)
	}
	if input.InferenceConfig.Temperature == nil || *input.InferenceConfig.Temperature != 0.3 {
		t.Fatalf("temperature = %#v, want 0.3", input.InferenceConfig.Temperature)
	}
	if input.InferenceConfig.TopP == nil || *input.InferenceConfig.TopP != 0.8 {
		t.Fatalf("topP = %#v, want 0.8", input.InferenceConfig.TopP)
	}
}

func TestAWSLLMChatForwardsReferenceAdditionalRequestFields(t *testing.T) {
	provider := &AWSLLM{
		client: fakeAWSLLMClient{err: errors.New("stop after capture")},
		model:  defaultAWSLLMModel,
	}
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	stream, err := provider.Chat(context.Background(), ctx, llm.WithExtraParams(map[string]any{
		"additional_request_fields": map[string]any{
			"thinking": map[string]any{"type": "disabled"},
		},
	}))
	if err != nil {
		t.Fatalf("Chat error = %v", err)
	}
	input := stream.(*awsLLMStream).request

	if input == nil || input.AdditionalModelRequestFields == nil {
		t.Fatalf("AdditionalModelRequestFields = %#v, want reference additional request fields", input)
	}
}

func TestAWSLLMChatForwardsReferenceProviderAdditionalRequestFields(t *testing.T) {
	provider := &AWSLLM{
		client: fakeAWSLLMClient{err: errors.New("stop after capture")},
		model:  defaultAWSLLMModel,
	}
	WithAWSLLMAdditionalRequestFields(map[string]any{
		"thinking": map[string]any{"type": "disabled"},
	})(provider)
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	stream, err := provider.Chat(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Chat error = %v", err)
	}
	input := stream.(*awsLLMStream).request

	if input == nil || input.AdditionalModelRequestFields == nil {
		t.Fatalf("AdditionalModelRequestFields = %#v, want provider additional request fields", input)
	}
}

func TestAWSLLMChatAddsReferenceProviderCachePoints(t *testing.T) {
	provider := &AWSLLM{
		client: fakeAWSLLMClient{err: errors.New("stop after capture")},
		model:  defaultAWSLLMModel,
	}
	WithAWSLLMCacheSystem(true)(provider)
	WithAWSLLMCacheTools(true)(provider)
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "system", Role: llm.ChatRoleSystem, Content: []llm.ChatContent{{Text: "be brief"}}},
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	stream, err := provider.Chat(context.Background(), ctx, llm.WithTools([]llm.Tool{awsRequestTestTool{}}))
	if err != nil {
		t.Fatalf("Chat error = %v", err)
	}
	input := stream.(*awsLLMStream).request

	if input == nil || len(input.System) != 2 {
		t.Fatalf("System = %#v, want system text plus cachePoint", input)
	}
	if _, ok := input.System[1].(*awstypes.SystemContentBlockMemberCachePoint); !ok {
		t.Fatalf("system cache block = %T, want cachePoint", input.System[1])
	}
	if input.ToolConfig == nil || len(input.ToolConfig.Tools) != 2 {
		t.Fatalf("ToolConfig = %#v, want tool plus cachePoint", input.ToolConfig)
	}
	if _, ok := input.ToolConfig.Tools[1].(*awstypes.ToolMemberCachePoint); !ok {
		t.Fatalf("tool cache block = %T, want cachePoint", input.ToolConfig.Tools[1])
	}
}

func TestAWSLLMStreamClosedState(t *testing.T) {
	stream := &awsLLMStream{closed: true}

	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next err = %v, want EOF when closed", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close err = %v, want nil without event stream", err)
	}
}

func TestAWSLLMStreamNextAfterCloseReturnsReferenceEOF(t *testing.T) {
	reader := newFakeAWSLLMReader()
	reader.err = errors.New("bedrock stream reset")
	stream := &awsLLMStream{
		stream: bedrockruntime.NewConverseStreamEventStream(func(es *bedrockruntime.ConverseStreamEventStream) {
			es.Reader = reader
		}),
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if !reader.closed {
		t.Fatal("provider stream closed = false, want Close to cancel Bedrock stream")
	}
	chunk, err := stream.Next()
	if chunk != nil {
		t.Fatalf("Next chunk = %#v, want nil after Close", chunk)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want EOF after Close", err)
	}
}

func TestAWSLLMStreamBuffersToolUseUntilContentBlockStop(t *testing.T) {
	reader := newFakeAWSLLMReader()
	reader.events <- &awstypes.ConverseStreamOutputMemberContentBlockStart{
		Value: awstypes.ContentBlockStartEvent{
			Start: &awstypes.ContentBlockStartMemberToolUse{
				Value: awstypes.ToolUseBlockStart{
					ToolUseId: awsString("call_lookup"),
					Name:      awsString("lookup"),
				},
			},
		},
	}
	reader.events <- &awstypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: awstypes.ContentBlockDeltaEvent{
			Delta: &awstypes.ContentBlockDeltaMemberToolUse{
				Value: awstypes.ToolUseBlockDelta{Input: awsString(`{"query"`)},
			},
		},
	}
	reader.events <- &awstypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: awstypes.ContentBlockDeltaEvent{
			Delta: &awstypes.ContentBlockDeltaMemberToolUse{
				Value: awstypes.ToolUseBlockDelta{Input: awsString(`:"weather"}`)},
			},
		},
	}
	reader.events <- &awstypes.ConverseStreamOutputMemberContentBlockStop{}
	close(reader.events)

	stream := &awsLLMStream{
		stream: bedrockruntime.NewConverseStreamEventStream(func(es *bedrockruntime.ConverseStreamEventStream) {
			es.Reader = reader
		}),
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want aggregated tool call", err)
	}
	if chunk == nil || chunk.Delta == nil || len(chunk.Delta.ToolCalls) != 1 {
		t.Fatalf("Next chunk = %#v, want one aggregated tool call", chunk)
	}
	call := chunk.Delta.ToolCalls[0]
	if call.CallID != "call_lookup" || call.Name != "lookup" || call.Type != "function" {
		t.Fatalf("tool call metadata = %+v, want lookup function call", call)
	}
	if call.Arguments != `{"query":"weather"}` {
		t.Fatalf("tool call arguments = %q, want aggregated JSON arguments", call.Arguments)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next after tool call err = %v, want EOF", err)
	}
}

func TestAWSLLMStreamRejectsReferenceContentBlockStartWithoutStart(t *testing.T) {
	reader := newFakeAWSLLMReader()
	reader.events <- &awstypes.ConverseStreamOutputMemberContentBlockStart{}
	close(reader.events)

	stream := &awsLLMStream{
		stream: bedrockruntime.NewConverseStreamEventStream(func(es *bedrockruntime.ConverseStreamEventStream) {
			es.Reader = reader
		}),
	}

	chunk, err := stream.Next()
	if chunk != nil {
		t.Fatalf("Next chunk = %#v, want nil for malformed contentBlockStart", chunk)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !connectionErr.Retryable {
		t.Fatal("Retryable = false, want true before any emitted chunk")
	}
}

func TestAWSLLMChatPreservesReferenceQueuedTextDeltas(t *testing.T) {
	reader := &fakeAWSLLMReader{events: make(chan awstypes.ConverseStreamOutput, 4)}
	provider := &AWSLLM{
		client: fakeAWSLLMClient{
			out: newFakeAWSLLMOutput(reader),
		},
		model: defaultAWSLLMModel,
	}
	chatCtx := llm.NewChatContext()
	chatCtx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}
	stream, err := provider.Chat(context.Background(), chatCtx)
	if err != nil {
		t.Fatalf("Chat error = %v", err)
	}

	const deltas = 12
	stop := make(chan struct{})
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		defer close(reader.events)
		for i := 0; i < deltas; i++ {
			event := &awstypes.ConverseStreamOutputMemberContentBlockDelta{
				Value: awstypes.ContentBlockDeltaEvent{
					Delta: &awstypes.ContentBlockDeltaMemberText{Value: fmt.Sprintf("delta-%02d", i)},
				},
			}
			select {
			case reader.events <- event:
			case <-stop:
				return
			}
		}
	}()

	select {
	case <-sent:
	case <-time.After(150 * time.Millisecond):
		close(stop)
		_ = stream.Close()
		t.Fatalf("AWS LLM provider stream blocked behind %d queued text deltas; want reference background drain into event channel", deltas)
	}

	for i := 0; i < deltas; i++ {
		chunk, err := stream.Next()
		if err != nil {
			t.Fatalf("Next delta %d error = %v", i, err)
		}
		want := fmt.Sprintf("delta-%02d", i)
		if chunk == nil || chunk.Delta == nil || chunk.Delta.Content != want {
			t.Fatalf("chunk %d = %#v, want text delta %q", i, chunk, want)
		}
	}
	if chunk, err := stream.Next(); err != io.EOF || chunk != nil {
		t.Fatalf("Next EOF = (%#v, %v), want nil EOF", chunk, err)
	}
}

func TestAWSLLMStreamRejectsReferenceToolUseWithoutName(t *testing.T) {
	reader := newFakeAWSLLMReader()
	reader.events <- &awstypes.ConverseStreamOutputMemberContentBlockStart{
		Value: awstypes.ContentBlockStartEvent{
			Start: &awstypes.ContentBlockStartMemberToolUse{
				Value: awstypes.ToolUseBlockStart{
					ToolUseId: awsString("call_lookup"),
				},
			},
		},
	}
	reader.events <- &awstypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: awstypes.ContentBlockDeltaEvent{
			Delta: &awstypes.ContentBlockDeltaMemberToolUse{
				Value: awstypes.ToolUseBlockDelta{Input: awsString(`{"query":"weather"}`)},
			},
		},
	}
	reader.events <- &awstypes.ConverseStreamOutputMemberContentBlockStop{}
	close(reader.events)

	stream := &awsLLMStream{
		stream: bedrockruntime.NewConverseStreamEventStream(func(es *bedrockruntime.ConverseStreamEventStream) {
			es.Reader = reader
		}),
	}

	chunk, err := stream.Next()
	if chunk != nil {
		t.Fatalf("Next chunk = %#v, want nil for malformed tool call", chunk)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !connectionErr.Retryable {
		t.Fatal("Retryable = false, want true before any emitted chunk")
	}
}

func TestAWSLLMStreamRejectsReferenceToolDeltaWithoutStart(t *testing.T) {
	reader := newFakeAWSLLMReader()
	reader.events <- &awstypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: awstypes.ContentBlockDeltaEvent{
			Delta: &awstypes.ContentBlockDeltaMemberToolUse{
				Value: awstypes.ToolUseBlockDelta{Input: awsString(`{"query":"weather"}`)},
			},
		},
	}
	close(reader.events)

	stream := &awsLLMStream{
		stream: bedrockruntime.NewConverseStreamEventStream(func(es *bedrockruntime.ConverseStreamEventStream) {
			es.Reader = reader
		}),
	}

	chunk, err := stream.Next()
	if chunk != nil {
		t.Fatalf("Next chunk = %#v, want nil for malformed tool delta", chunk)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !connectionErr.Retryable {
		t.Fatal("Retryable = false, want true before any emitted chunk")
	}
}

func TestAWSLLMStreamRejectsReferenceToolDeltaWithoutInput(t *testing.T) {
	reader := newFakeAWSLLMReader()
	reader.events <- &awstypes.ConverseStreamOutputMemberContentBlockStart{
		Value: awstypes.ContentBlockStartEvent{
			Start: &awstypes.ContentBlockStartMemberToolUse{
				Value: awstypes.ToolUseBlockStart{
					ToolUseId: awsString("call_lookup"),
					Name:      awsString("lookup"),
				},
			},
		},
	}
	reader.events <- &awstypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: awstypes.ContentBlockDeltaEvent{
			Delta: &awstypes.ContentBlockDeltaMemberToolUse{
				Value: awstypes.ToolUseBlockDelta{},
			},
		},
	}
	close(reader.events)

	stream := &awsLLMStream{
		stream: bedrockruntime.NewConverseStreamEventStream(func(es *bedrockruntime.ConverseStreamEventStream) {
			es.Reader = reader
		}),
	}

	chunk, err := stream.Next()
	if chunk != nil {
		t.Fatalf("Next chunk = %#v, want nil for malformed tool delta", chunk)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !connectionErr.Retryable {
		t.Fatal("Retryable = false, want true before any emitted chunk")
	}
}

func TestAWSLLMStreamChunksCarryReferenceRequestID(t *testing.T) {
	reader := newFakeAWSLLMReader()
	reader.events <- &awstypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: awstypes.ContentBlockDeltaEvent{
			Delta: &awstypes.ContentBlockDeltaMemberText{Value: "hello"},
		},
	}
	close(reader.events)

	stream := &awsLLMStream{
		stream: bedrockruntime.NewConverseStreamEventStream(func(es *bedrockruntime.ConverseStreamEventStream) {
			es.Reader = reader
		}),
		requestID: "aws-request-1",
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want text chunk", err)
	}
	if chunk.ID != "aws-request-1" {
		t.Fatalf("chunk ID = %q, want reference request ID", chunk.ID)
	}
}

func TestAWSLLMStreamMapsReferenceCacheReadUsage(t *testing.T) {
	reader := newFakeAWSLLMReader()
	reader.events <- &awstypes.ConverseStreamOutputMemberMetadata{
		Value: awstypes.ConverseStreamMetadataEvent{
			Usage: &awstypes.TokenUsage{
				InputTokens:          awsInt32(11),
				OutputTokens:         awsInt32(7),
				TotalTokens:          awsInt32(18),
				CacheReadInputTokens: awsInt32(5),
			},
		},
	}
	close(reader.events)

	stream := &awsLLMStream{
		stream: bedrockruntime.NewConverseStreamEventStream(func(es *bedrockruntime.ConverseStreamEventStream) {
			es.Reader = reader
		}),
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want usage chunk", err)
	}
	if chunk == nil || chunk.Usage == nil {
		t.Fatalf("Next chunk = %#v, want usage", chunk)
	}
	if chunk.Usage.PromptTokens != 11 || chunk.Usage.CompletionTokens != 7 || chunk.Usage.TotalTokens != 18 {
		t.Fatalf("usage = %+v, want prompt/completion/total token counts", chunk.Usage)
	}
	if chunk.Usage.PromptCachedTokens != 5 {
		t.Fatalf("prompt cached tokens = %d, want cacheReadInputTokens", chunk.Usage.PromptCachedTokens)
	}
}

func TestAWSLLMStreamUsageChunkIsReferenceUsageOnly(t *testing.T) {
	reader := newFakeAWSLLMReader()
	reader.events <- &awstypes.ConverseStreamOutputMemberMetadata{
		Value: awstypes.ConverseStreamMetadataEvent{
			Usage: &awstypes.TokenUsage{
				InputTokens:  awsInt32(1),
				OutputTokens: awsInt32(2),
				TotalTokens:  awsInt32(3),
			},
		},
	}
	close(reader.events)

	stream := &awsLLMStream{
		stream: bedrockruntime.NewConverseStreamEventStream(func(es *bedrockruntime.ConverseStreamEventStream) {
			es.Reader = reader
		}),
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want usage chunk", err)
	}
	if chunk == nil || chunk.Usage == nil {
		t.Fatalf("Next chunk = %#v, want usage", chunk)
	}
	if chunk.Delta != nil {
		t.Fatalf("usage chunk delta = %#v, want nil like reference metadata chunk", chunk.Delta)
	}
}

func TestAWSLLMStreamRejectsReferenceMetadataWithoutUsage(t *testing.T) {
	reader := newFakeAWSLLMReader()
	reader.events <- &awstypes.ConverseStreamOutputMemberMetadata{}
	close(reader.events)

	stream := &awsLLMStream{
		stream: bedrockruntime.NewConverseStreamEventStream(func(es *bedrockruntime.ConverseStreamEventStream) {
			es.Reader = reader
		}),
	}

	chunk, err := stream.Next()
	if chunk != nil {
		t.Fatalf("Next chunk = %#v, want nil for malformed metadata", chunk)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !connectionErr.Retryable {
		t.Fatal("Retryable = false, want true before any emitted chunk")
	}
}

func TestAWSLLMStreamRejectsReferenceMetadataWithoutTokenCounts(t *testing.T) {
	reader := newFakeAWSLLMReader()
	reader.events <- &awstypes.ConverseStreamOutputMemberMetadata{
		Value: awstypes.ConverseStreamMetadataEvent{
			Usage: &awstypes.TokenUsage{CacheReadInputTokens: awsInt32(2)},
		},
	}
	close(reader.events)

	stream := &awsLLMStream{
		stream: bedrockruntime.NewConverseStreamEventStream(func(es *bedrockruntime.ConverseStreamEventStream) {
			es.Reader = reader
		}),
	}

	chunk, err := stream.Next()
	if chunk != nil {
		t.Fatalf("Next chunk = %#v, want nil for malformed usage", chunk)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !connectionErr.Retryable {
		t.Fatal("Retryable = false, want true before any emitted chunk")
	}
}

func TestAWSLLMStreamKeepsUsageAfterMessageStop(t *testing.T) {
	reader := newFakeAWSLLMReader()
	reader.events <- &awstypes.ConverseStreamOutputMemberMessageStop{}
	reader.events <- &awstypes.ConverseStreamOutputMemberMetadata{
		Value: awstypes.ConverseStreamMetadataEvent{
			Usage: &awstypes.TokenUsage{
				InputTokens:  awsInt32(3),
				OutputTokens: awsInt32(4),
				TotalTokens:  awsInt32(7),
			},
		},
	}
	close(reader.events)

	stream := &awsLLMStream{
		stream: bedrockruntime.NewConverseStreamEventStream(func(es *bedrockruntime.ConverseStreamEventStream) {
			es.Reader = reader
		}),
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want usage after messageStop", err)
	}
	if chunk == nil || chunk.Usage == nil {
		t.Fatalf("Next chunk = %#v, want usage after messageStop", chunk)
	}
	if chunk.Usage.PromptTokens != 3 || chunk.Usage.CompletionTokens != 4 || chunk.Usage.TotalTokens != 7 {
		t.Fatalf("usage = %+v, want metadata after messageStop", chunk.Usage)
	}
}

func TestAWSLLMStreamErrorReturnsAPIConnectionError(t *testing.T) {
	reader := newFakeAWSLLMReader()
	reader.err = errors.New("bedrock stream reset")
	close(reader.events)

	stream := &awsLLMStream{
		stream: bedrockruntime.NewConverseStreamEventStream(func(es *bedrockruntime.ConverseStreamEventStream) {
			es.Reader = reader
		}),
	}

	chunk, err := stream.Next()

	if chunk != nil {
		t.Fatalf("Next chunk = %#v, want nil", chunk)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(err.Error(), "AWS Bedrock LLM stream failed") {
		t.Fatalf("Next error = %q, want AWS Bedrock stream context", err.Error())
	}
}

func TestAWSLLMStreamDeadlineReturnsAPITimeoutError(t *testing.T) {
	reader := newFakeAWSLLMReader()
	reader.err = context.DeadlineExceeded
	close(reader.events)

	stream := &awsLLMStream{
		stream: bedrockruntime.NewConverseStreamEventStream(func(es *bedrockruntime.ConverseStreamEventStream) {
			es.Reader = reader
		}),
	}

	chunk, err := stream.Next()

	if chunk != nil {
		t.Fatalf("Next chunk = %#v, want nil on stream timeout", chunk)
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next error = %T %v, want APITimeoutError", err, err)
	}
}

func TestAWSLLMStreamErrorAfterChunkIsReferenceNonRetryable(t *testing.T) {
	reader := newFakeAWSLLMReader()
	reader.events <- &awstypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: awstypes.ContentBlockDeltaEvent{
			Delta: &awstypes.ContentBlockDeltaMemberText{Value: "hello"},
		},
	}
	reader.err = errors.New("bedrock stream reset")
	close(reader.events)

	stream := &awsLLMStream{
		stream: bedrockruntime.NewConverseStreamEventStream(func(es *bedrockruntime.ConverseStreamEventStream) {
			es.Reader = reader
		}),
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v, want text chunk before stream error", err)
	}
	if chunk == nil || chunk.Delta == nil || chunk.Delta.Content != "hello" {
		t.Fatalf("first Next chunk = %#v, want hello text delta", chunk)
	}

	chunk, err = stream.Next()
	if chunk != nil {
		t.Fatalf("second Next chunk = %#v, want nil", chunk)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("second Next error = %T %v, want APIConnectionError", err, err)
	}
	if connectionErr.Retryable {
		t.Fatal("Retryable = true, want false after partial reference chunk")
	}
}

func TestAWSLLMStreamMessageStopReturnsEOFWithoutEmptyChunk(t *testing.T) {
	reader := newFakeAWSLLMReader()
	reader.events <- &awstypes.ConverseStreamOutputMemberMessageStop{}
	close(reader.events)

	stream := &awsLLMStream{
		stream: bedrockruntime.NewConverseStreamEventStream(func(es *bedrockruntime.ConverseStreamEventStream) {
			es.Reader = reader
		}),
	}

	chunk, err := stream.Next()

	if chunk != nil {
		t.Fatalf("Next chunk = %#v, want nil empty terminal chunk suppressed", chunk)
	}
	if err != io.EOF {
		t.Fatalf("Next error = %v, want EOF", err)
	}
}

func TestBuildAWSMessagesGroupsToolCallsWithOutputs(t *testing.T) {
	ctx := llm.NewChatContext()
	groupID := "assistant-turn"
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: groupID, Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "checking"}}},
		&llm.FunctionCall{ID: groupID + "/tool-1", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCall{ID: groupID + "/tool-2", CallID: "call_weather", Name: "weather", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCallOutput{ID: "lookup-output", CallID: "call_lookup", Name: "lookup", Output: "Paris"},
		&llm.FunctionCallOutput{ID: "weather-output", CallID: "call_weather", Name: "weather", Output: "sunny"},
	}

	messages, systemText := buildAWSMessages(ctx)

	if systemText != "" {
		t.Fatalf("systemText = %q, want empty", systemText)
	}
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3: %#v", len(messages), messages)
	}
	if messages[0].Role != awstypes.ConversationRoleUser {
		t.Fatalf("first role = %q, want user", messages[0].Role)
	}
	assertTextBlock(t, messages[0].Content, 0, "(empty)")
	if messages[1].Role != awstypes.ConversationRoleAssistant {
		t.Fatalf("second role = %q, want assistant", messages[1].Role)
	}
	assertTextBlock(t, messages[1].Content, 0, "checking")
	assertToolUseBlock(t, messages[1].Content, 1, "call_lookup", "lookup")
	assertToolUseBlock(t, messages[1].Content, 2, "call_weather", "weather")
	if messages[2].Role != awstypes.ConversationRoleUser {
		t.Fatalf("third role = %q, want user", messages[2].Role)
	}
	assertToolResultBlock(t, messages[2].Content, 0, "call_lookup", awstypes.ToolResultStatusSuccess)
	assertToolResultBlock(t, messages[2].Content, 1, "call_weather", awstypes.ToolResultStatusSuccess)
}

func TestBuildAWSMessagesMapsReferenceToolResultText(t *testing.T) {
	ctx := llm.NewChatContext()
	groupID := "assistant-turn"
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: groupID, Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "checking"}}},
		&llm.FunctionCall{ID: groupID + "/tool-1", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCallOutput{ID: "lookup-output", CallID: "call_lookup", Name: "lookup", Output: "sunny"},
	}

	messages, _ := buildAWSMessages(ctx)

	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3", len(messages))
	}
	block, ok := messages[2].Content[0].(*awstypes.ContentBlockMemberToolResult)
	if !ok {
		t.Fatalf("tool result block = %T, want ToolResult", messages[2].Content[0])
	}
	if len(block.Value.Content) != 1 {
		t.Fatalf("tool result content len = %d, want 1", len(block.Value.Content))
	}
	text, ok := block.Value.Content[0].(*awstypes.ToolResultContentBlockMemberText)
	if !ok {
		t.Fatalf("tool result content = %T, want reference text content", block.Value.Content[0])
	}
	if text.Value != "sunny" {
		t.Fatalf("tool result text = %q, want sunny", text.Value)
	}
}

func TestBuildAWSMessagesPreservesReferenceMultipleToolOutputs(t *testing.T) {
	ctx := llm.NewChatContext()
	groupID := "assistant-turn"
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: groupID, Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "checking"}}},
		&llm.FunctionCall{ID: groupID + "/tool-1", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCallOutput{ID: "lookup-output-1", CallID: "call_lookup", Name: "lookup", Output: "first"},
		&llm.FunctionCallOutput{ID: "lookup-output-2", CallID: "call_lookup", Name: "lookup", Output: "second"},
	}

	messages, _ := buildAWSMessages(ctx)

	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3", len(messages))
	}
	if len(messages[2].Content) != 2 {
		t.Fatalf("tool result blocks = %d, want both reference outputs", len(messages[2].Content))
	}
	assertToolResultTextBlock(t, messages[2].Content, 0, "call_lookup", "first")
	assertToolResultTextBlock(t, messages[2].Content, 1, "call_lookup", "second")
}

func TestBuildAWSMessagesKeepsReferenceToolResultStatusSuccessForErrors(t *testing.T) {
	ctx := llm.NewChatContext()
	groupID := "assistant-turn"
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: groupID, Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "checking"}}},
		&llm.FunctionCall{ID: groupID + "/tool-1", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCallOutput{ID: "lookup-output", CallID: "call_lookup", Name: "lookup", Output: "error: timeout", IsError: true},
	}

	messages, _ := buildAWSMessages(ctx)

	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3", len(messages))
	}
	block, ok := messages[2].Content[0].(*awstypes.ContentBlockMemberToolResult)
	if !ok {
		t.Fatalf("tool result block = %T, want ToolResult", messages[2].Content[0])
	}
	if block.Value.Status != awstypes.ToolResultStatusSuccess {
		t.Fatalf("tool result status = %q, want reference success", block.Value.Status)
	}
}

func TestBuildAWSMessagesFiltersUnmatchedToolItems(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
		&llm.FunctionCall{ID: "orphan-call", CallID: "call_missing_output", Name: "lookup", Arguments: `{}`},
		&llm.FunctionCallOutput{ID: "orphan-output", CallID: "call_missing_call", Name: "lookup", Output: "ignored"},
	}

	messages, _ := buildAWSMessages(ctx)

	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1: %#v", len(messages), messages)
	}
	if messages[0].Role != awstypes.ConversationRoleUser {
		t.Fatalf("role = %q, want user", messages[0].Role)
	}
	assertTextBlock(t, messages[0].Content, 0, "hello")
}

func TestBuildAWSMessagesIncludesInlineImageBlocks(t *testing.T) {
	imageData := base64.StdEncoding.EncodeToString([]byte("webp-bytes"))
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{
			ID:   "user",
			Role: llm.ChatRoleUser,
			Content: []llm.ChatContent{
				{Text: "describe"},
				{Image: &llm.ImageContent{Image: "data:image/webp;base64," + imageData}},
			},
		},
	}

	messages, _ := buildAWSMessages(ctx)

	if len(messages[0].Content) != 2 {
		t.Fatalf("len(content) = %d, want 2: %#v", len(messages[0].Content), messages[0].Content)
	}
	assertTextBlock(t, messages[0].Content, 0, "describe")
	imageBlock, ok := messages[0].Content[1].(*awstypes.ContentBlockMemberImage)
	if !ok {
		t.Fatalf("image content = %#v, want ContentBlockMemberImage", messages[0].Content[1])
	}
	if imageBlock.Value.Format != awstypes.ImageFormatJpeg {
		t.Fatalf("image format = %q, want jpeg like reference AWS formatter", imageBlock.Value.Format)
	}
	source, ok := imageBlock.Value.Source.(*awstypes.ImageSourceMemberBytes)
	if !ok || !reflect.DeepEqual(source.Value, []byte("webp-bytes")) {
		t.Fatalf("image source = %#v, want bytes", imageBlock.Value.Source)
	}
}

func TestAWSLLMChatRejectsReferenceExternalImage(t *testing.T) {
	var captured *bedrockruntime.ConverseStreamInput
	provider := &AWSLLM{
		client: fakeAWSLLMClient{
			err:          errors.New("bedrock should not be called"),
			inputCapture: &captured,
		},
		model: defaultAWSLLMModel,
	}
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{
			ID:   "user",
			Role: llm.ChatRoleUser,
			Content: []llm.ChatContent{
				{Text: "describe"},
				{Image: &llm.ImageContent{Image: "https://example.test/image.png"}},
			},
		},
	}

	stream, err := provider.Chat(context.Background(), ctx)

	if stream != nil {
		t.Fatalf("Chat stream = %#v, want nil for external image", stream)
	}
	if err == nil || !strings.Contains(err.Error(), "external_url is not supported by AWS Bedrock") {
		t.Fatalf("Chat error = %v, want reference external_url unsupported error", err)
	}
	if captured != nil {
		t.Fatalf("ConverseStream input = %#v, want no provider call for unsupported external image", captured)
	}
}

func TestBuildAWSToolConfigMapsNamedToolChoice(t *testing.T) {
	config := buildAWSToolConfig(&llm.ChatOptions{
		Tools: []llm.Tool{awsRequestTestTool{}},
		ToolChoice: map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": "lookup",
			},
		},
	})

	if config == nil {
		t.Fatalf("tool config is nil")
	}
	choice, ok := config.ToolChoice.(*awstypes.ToolChoiceMemberTool)
	if !ok {
		t.Fatalf("ToolChoice = %T, want tool member", config.ToolChoice)
	}
	if choice.Value.Name == nil || *choice.Value.Name != "lookup" {
		t.Fatalf("ToolChoice name = %#v, want lookup", choice.Value.Name)
	}
}

func TestAWSLLMChatUsesReferenceProviderToolChoice(t *testing.T) {
	provider := &AWSLLM{
		client: fakeAWSLLMClient{err: errors.New("stop after capture")},
		model:  defaultAWSLLMModel,
	}
	WithAWSLLMToolChoice("required")(provider)
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	stream, err := provider.Chat(context.Background(), ctx, llm.WithTools([]llm.Tool{awsRequestTestTool{}}))
	if err != nil {
		t.Fatalf("Chat error = %v", err)
	}
	input := stream.(*awsLLMStream).request

	if input == nil || input.ToolConfig == nil {
		t.Fatalf("ToolConfig = %#v, want reference provider tool choice", input)
	}
	if _, ok := input.ToolConfig.ToolChoice.(*awstypes.ToolChoiceMemberAny); !ok {
		t.Fatalf("ToolChoice = %T, want required/any from provider default", input.ToolConfig.ToolChoice)
	}
}

func TestBuildAWSToolConfigDropsToolsForNoneChoice(t *testing.T) {
	config := buildAWSToolConfig(&llm.ChatOptions{
		Tools:      []llm.Tool{awsRequestTestTool{}},
		ToolChoice: "none",
	})

	if config != nil {
		t.Fatalf("tool config = %#v, want nil", config)
	}
}

func TestAWSLLMChatToolChoiceNoneStripsReferenceFunctionHistory(t *testing.T) {
	provider := &AWSLLM{
		client: fakeAWSLLMClient{err: errors.New("stop after capture")},
		model:  defaultAWSLLMModel,
	}
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
		&llm.FunctionCall{ID: "call", CallID: "call_lookup", Name: "lookup", Arguments: `{}`},
		&llm.FunctionCallOutput{ID: "output", CallID: "call_lookup", Name: "lookup", Output: "sunny"},
	}

	stream, err := provider.Chat(
		context.Background(),
		ctx,
		llm.WithTools([]llm.Tool{awsRequestTestTool{}}),
		llm.WithToolChoice("none"),
	)
	if err != nil {
		t.Fatalf("Chat error = %v", err)
	}
	input := stream.(*awsLLMStream).request

	if input == nil {
		t.Fatal("captured request = nil")
	}
	if input.ToolConfig != nil {
		t.Fatalf("ToolConfig = %#v, want nil for reference none tool choice", input.ToolConfig)
	}
	for _, msg := range input.Messages {
		for _, block := range msg.Content {
			switch block.(type) {
			case *awstypes.ContentBlockMemberToolUse, *awstypes.ContentBlockMemberToolResult:
				t.Fatalf("message content includes %T, want function history stripped when toolConfig is nil", block)
			}
		}
	}
}

func TestBuildAWSToolConfigOmitsReferenceEmptyDescription(t *testing.T) {
	config := buildAWSToolConfig(&llm.ChatOptions{
		Tools: []llm.Tool{awsEmptyDescriptionTool{}},
	})

	if config == nil || len(config.Tools) != 1 {
		t.Fatalf("tool config = %#v, want one tool", config)
	}
	tool, ok := config.Tools[0].(*awstypes.ToolMemberToolSpec)
	if !ok {
		t.Fatalf("tool = %T, want ToolSpec", config.Tools[0])
	}
	if tool.Value.Description != nil {
		t.Fatalf("tool description = %#v, want nil for empty reference description", tool.Value.Description)
	}
}

func TestBuildAWSMessagesCollectsSystemText(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "system", Role: llm.ChatRoleSystem, Content: []llm.ChatContent{{Text: "base"}}},
		&llm.ChatMessage{ID: "developer", Role: llm.ChatRoleDeveloper, Content: []llm.ChatContent{{Text: "dev"}}},
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	messages, systemText := buildAWSMessages(ctx)

	if systemText != "base" {
		t.Fatalf("systemText = %q, want base", systemText)
	}
	if strings.HasSuffix(systemText, "\n") {
		t.Fatalf("systemText = %q, want no reference trailing newline", systemText)
	}
	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(messages))
	}
	assertTextBlock(t, messages[0].Content, 0, "<instructions>\ndev\n</instructions>")
	assertTextBlock(t, messages[0].Content, 1, "hello")
}

func TestBuildAWSMessagesKeepsReferenceDeveloperPreambleAsUserTurn(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "developer", Role: llm.ChatRoleDeveloper, Content: []llm.ChatContent{{Text: "dev preamble"}}},
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	messages, systemText := buildAWSMessages(ctx)

	if systemText != "" {
		t.Fatalf("systemText = %q, want empty because reference AWS formatter only extracts system role", systemText)
	}
	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want one merged user turn", len(messages))
	}
	if messages[0].Role != awstypes.ConversationRoleUser {
		t.Fatalf("role = %q, want user", messages[0].Role)
	}
	assertTextBlock(t, messages[0].Content, 0, "dev preamble")
	assertTextBlock(t, messages[0].Content, 1, "hello")
}

func TestBuildAWSMessagesConvertsReferenceMidConversationInstructions(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "system", Role: llm.ChatRoleSystem, Content: []llm.ChatContent{{Text: "base"}}},
		&llm.ChatMessage{ID: "user-1", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
		&llm.ChatMessage{ID: "assistant-1", Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "hi"}}},
		&llm.ChatMessage{ID: "system-2", Role: llm.ChatRoleSystem, Content: []llm.ChatContent{{Text: "answer tersely"}}},
		&llm.ChatMessage{ID: "user-2", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "weather?"}}},
	}

	messages, systemText := buildAWSMessages(ctx)

	if systemText != "base" {
		t.Fatalf("systemText = %q, want only first system message", systemText)
	}
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3: %#v", len(messages), messages)
	}
	if messages[2].Role != awstypes.ConversationRoleUser {
		t.Fatalf("mid-instruction role = %q, want user", messages[2].Role)
	}
	assertTextBlock(t, messages[2].Content, 0, "<instructions>\nanswer tersely\n</instructions>")
	assertTextBlock(t, messages[2].Content, 1, "weather?")
}

func assertTextBlock(t *testing.T, blocks []awstypes.ContentBlock, index int, want string) {
	t.Helper()
	if len(blocks) <= index {
		t.Fatalf("len(blocks) = %d, want index %d", len(blocks), index)
	}
	block, ok := blocks[index].(*awstypes.ContentBlockMemberText)
	if !ok {
		t.Fatalf("block[%d] = %T, want text", index, blocks[index])
	}
	if block.Value != want {
		t.Fatalf("text block[%d] = %q, want %q", index, block.Value, want)
	}
}

func assertToolUseBlock(t *testing.T, blocks []awstypes.ContentBlock, index int, wantID, wantName string) {
	t.Helper()
	if len(blocks) <= index {
		t.Fatalf("len(blocks) = %d, want index %d", len(blocks), index)
	}
	block, ok := blocks[index].(*awstypes.ContentBlockMemberToolUse)
	if !ok {
		t.Fatalf("block[%d] = %T, want tool use", index, blocks[index])
	}
	if block.Value.ToolUseId == nil || *block.Value.ToolUseId != wantID {
		t.Fatalf("tool use id = %#v, want %q", block.Value.ToolUseId, wantID)
	}
	if block.Value.Name == nil || *block.Value.Name != wantName {
		t.Fatalf("tool use name = %#v, want %q", block.Value.Name, wantName)
	}
}

func assertToolResultBlock(t *testing.T, blocks []awstypes.ContentBlock, index int, wantID string, wantStatus awstypes.ToolResultStatus) {
	t.Helper()
	if len(blocks) <= index {
		t.Fatalf("len(blocks) = %d, want index %d", len(blocks), index)
	}
	block, ok := blocks[index].(*awstypes.ContentBlockMemberToolResult)
	if !ok {
		t.Fatalf("block[%d] = %T, want tool result", index, blocks[index])
	}
	if block.Value.ToolUseId == nil || *block.Value.ToolUseId != wantID {
		t.Fatalf("tool result id = %#v, want %q", block.Value.ToolUseId, wantID)
	}
	if block.Value.Status != wantStatus {
		t.Fatalf("tool result status = %q, want %q", block.Value.Status, wantStatus)
	}
}

func assertToolResultTextBlock(t *testing.T, blocks []awstypes.ContentBlock, index int, wantID, wantText string) {
	t.Helper()
	assertToolResultBlock(t, blocks, index, wantID, awstypes.ToolResultStatusSuccess)
	block := blocks[index].(*awstypes.ContentBlockMemberToolResult)
	if len(block.Value.Content) != 1 {
		t.Fatalf("tool result content len = %d, want 1", len(block.Value.Content))
	}
	text, ok := block.Value.Content[0].(*awstypes.ToolResultContentBlockMemberText)
	if !ok {
		t.Fatalf("tool result content = %T, want text", block.Value.Content[0])
	}
	if text.Value != wantText {
		t.Fatalf("tool result text = %q, want %q", text.Value, wantText)
	}
}

type fakeAWSLLMReader struct {
	events chan awstypes.ConverseStreamOutput
	err    error
	closed bool
}

type fakeAWSLLMClient struct {
	out          *bedrockruntime.ConverseStreamOutput
	err          error
	ctxCapture   *context.Context
	inputCapture **bedrockruntime.ConverseStreamInput
}

type blockingAWSLLMClient struct {
	started chan struct{}
	release chan struct{}
}

type lateAWSLLMClient struct {
	started chan struct{}
	release chan struct{}
	out     *bedrockruntime.ConverseStreamOutput
}

func (c fakeAWSLLMClient) ConverseStream(ctx context.Context, input *bedrockruntime.ConverseStreamInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error) {
	if c.ctxCapture != nil {
		*c.ctxCapture = ctx
	}
	if c.inputCapture != nil {
		*c.inputCapture = input
	}
	if c.err != nil {
		return nil, c.err
	}
	return c.out, nil
}

func (c *lateAWSLLMClient) ConverseStream(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error) {
	close(c.started)
	<-c.release
	return c.out, nil
}

func (c *blockingAWSLLMClient) ConverseStream(ctx context.Context, _ *bedrockruntime.ConverseStreamInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error) {
	close(c.started)
	select {
	case <-c.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return newFakeAWSLLMOutput(&fakeAWSLLMReader{events: make(chan awstypes.ConverseStreamOutput)}), nil
}

func newFakeAWSLLMReader() *fakeAWSLLMReader {
	return &fakeAWSLLMReader{events: make(chan awstypes.ConverseStreamOutput, 8)}
}

func newFakeAWSLLMOutput(reader *fakeAWSLLMReader) *bedrockruntime.ConverseStreamOutput {
	out := &bedrockruntime.ConverseStreamOutput{}
	stream := bedrockruntime.NewConverseStreamEventStream(func(es *bedrockruntime.ConverseStreamEventStream) {
		es.Reader = reader
	})
	field := reflect.ValueOf(out).Elem().FieldByName("eventStream")
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(stream))
	return out
}

func (r *fakeAWSLLMReader) Events() <-chan awstypes.ConverseStreamOutput {
	return r.events
}

func (r *fakeAWSLLMReader) Close() error {
	r.closed = true
	return nil
}

func (r *fakeAWSLLMReader) Err() error {
	return r.err
}

func awsString(value string) *string {
	return &value
}

func awsInt32(value int32) *int32 {
	return &value
}
