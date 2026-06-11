package openai

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/logger"
	cavosmath "github.com/cavos-io/rtp-agent/library/math"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/utils"
	"github.com/cavos-io/rtp-agent/library/utils/images"
	"github.com/gorilla/websocket"
)

type RealtimeModel struct {
	apiKey        string
	model         string
	baseURL       string
	dialWebsocket openAIRealtimeWebsocketDialer
	mu            sync.Mutex
	options       llm.RealtimeSessionOptions
	modalities    []string
	sessions      map[*realtimeSession]struct{}
}

type openAIRealtimeWebsocketDialer func(string, http.Header) (*websocket.Conn, *http.Response, error)

type openAIRealtimeModelOptions struct {
	sessionOptions llm.RealtimeSessionOptions
	modalities     []string
	baseURL        string
}

type OpenAIRealtimeOption func(*openAIRealtimeModelOptions)

func WithOpenAIRealtimeVoice(voice string) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.sessionOptions.Voice = voice
	}
}

func WithOpenAIRealtimeSpeed(speed float64) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.sessionOptions.Speed = speed
		options.sessionOptions.SpeedSet = true
	}
}

func WithOpenAIRealtimeToolChoice(toolChoice llm.ToolChoice) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.sessionOptions.ToolChoice = toolChoice
		options.sessionOptions.ToolChoiceSet = true
	}
}

func WithOpenAIRealtimeTracing(tracing any) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.sessionOptions.Tracing = tracing
		options.sessionOptions.TracingSet = true
	}
}

func WithOpenAIRealtimeTruncation(truncation any) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.sessionOptions.Truncation = truncation
		options.sessionOptions.TruncationSet = true
	}
}

func WithOpenAIRealtimeInputAudioTranscription(transcription any) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.sessionOptions.InputAudioTranscription = transcription
		options.sessionOptions.InputAudioTranscriptionSet = true
	}
}

func WithOpenAIRealtimeInputAudioNoiseReduction(noiseReduction any) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.sessionOptions.InputAudioNoiseReduction = noiseReduction
		options.sessionOptions.InputAudioNoiseReductionSet = true
	}
}

func WithOpenAIRealtimeTurnDetection(turnDetection any) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.sessionOptions.TurnDetection = turnDetection
		options.sessionOptions.TurnDetectionSet = true
	}
}

func WithOpenAIRealtimeModalities(modalities []string) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.modalities = append([]string(nil), modalities...)
	}
}

func WithOpenAIRealtimeMaxResponseOutputTokens(tokens any) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.sessionOptions.MaxResponseOutputTokens = tokens
		options.sessionOptions.MaxResponseOutputTokensSet = true
	}
}

func WithOpenAIRealtimeBaseURL(baseURL string) OpenAIRealtimeOption {
	return func(options *openAIRealtimeModelOptions) {
		options.baseURL = baseURL
	}
}

func NewRealtimeModel(apiKey, model string, opts ...OpenAIRealtimeOption) *RealtimeModel {
	if model == "" {
		model = "gpt-realtime"
	}
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	options := openAIRealtimeModelOptions{}
	for _, opt := range opts {
		opt(&options)
	}
	if options.baseURL != "" {
		baseURL = options.baseURL
	}
	return &RealtimeModel{
		apiKey:        apiKey,
		model:         model,
		baseURL:       openAIRealtimeBaseURL(baseURL),
		dialWebsocket: defaultOpenAIRealtimeWebsocketDialer,
		options:       options.sessionOptions,
		modalities:    options.modalities,
	}
}

func (m *RealtimeModel) Model() string {
	return m.model
}

func (m *RealtimeModel) Provider() string {
	u, err := url.Parse(m.baseURL)
	if err != nil || u.Host == "" {
		return "openai"
	}
	return u.Host
}

func (m *RealtimeModel) Close() error {
	return nil
}

func (m *RealtimeModel) UpdateOptions(options llm.RealtimeSessionOptions) error {
	m.mu.Lock()
	openAIRealtimeMergeSessionOptions(&m.options, options)
	sessions := make([]*realtimeSession, 0, len(m.sessions))
	for session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.mu.Unlock()

	for _, session := range sessions {
		if err := session.UpdateOptions(options); err != nil {
			return err
		}
	}
	return nil
}

func (m *RealtimeModel) Capabilities() llm.RealtimeCapabilities {
	return llm.RealtimeCapabilities{
		MessageTruncation:       true,
		TurnDetection:           true,
		UserTranscription:       true,
		AutoToolReplyGeneration: false,
		AudioOutput:             len(m.modalities) == 0 || realtimeModalitiesInclude(m.modalities, "audio"),
		ManualFunctionCalls:     true,
		MutableChatContext:      true,
		MutableInstructions:     true,
		MutableTools:            true,
		PerResponseToolChoice:   true,
	}
}

type realtimeSession struct {
	model            *RealtimeModel
	conn             *websocket.Conn
	ctx              context.Context
	cancel           context.CancelFunc
	mu               sync.Mutex
	eventCh          chan llm.RealtimeEvent
	remote           *llm.RemoteChatContext
	inputTranscripts *utils.BoundedDict[inputTranscriptKey, realtimeInputTranscript]
	generation       *realtimeGeneration
	pendingResponses int
	instructions     string
	audioBStream     *audio.AudioByteStream
	pushedDuration   float64
	optionsState     map[string]any
}

const maxRealtimeInputTranscripts = 1024
const openAIRealtimeInputSampleRate = 24000
const openAIRealtimeInputNumChannels = 1
const openAIRealtimeDefaultVoice = "marin"
const openAIRealtimeDefaultSpeed = 1.0
const openAIRealtimeDefaultMaxOutputTokens = "inf"

type inputTranscriptKey struct {
	itemID       string
	contentIndex int
}

type realtimeInputTranscript struct {
	itemID       string
	contentIndex int
	transcript   string
}

type realtimeGeneration struct {
	messageCh  chan llm.MessageGeneration
	functionCh chan *llm.FunctionCall
	messages   map[string]*realtimeMessageGeneration
}

type realtimeMessageGeneration struct {
	textCh        chan string
	audioCh       chan *model.AudioFrame
	modalitiesCh  chan []string
	modalities    []string
	streamsClosed bool
}

func (m *RealtimeModel) Session() (llm.RealtimeSession, error) {
	if m.apiKey == "" {
		return nil, fmt.Errorf("%s", "The api_key client option must be set either by passing api_key to the client or by setting the OPENAI_API_KEY environment variable")
	}
	wsURL := openAIRealtimeSessionURL(m.baseURL, m.model)

	header := http.Header{}
	header.Add("Authorization", "Bearer "+m.apiKey)
	header.Add("OpenAI-Beta", "realtime=v1")

	conn, _, err := m.dialWebsocket(wsURL, header)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to OpenAI realtime: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	initialSession := openAIRealtimeInitialSession(m.model, m.modalities)
	m.mu.Lock()
	initialOptions := m.options
	m.mu.Unlock()
	if msg := openAIRealtimeUpdateOptionsMessage(initialOptions); msg != nil {
		if session, ok := msg["session"].(map[string]any); ok {
			openAIRealtimeMergeSessionPayload(initialSession, session)
		}
	}
	s := &realtimeSession{
		model:        m,
		conn:         conn,
		ctx:          ctx,
		cancel:       cancel,
		eventCh:      make(chan llm.RealtimeEvent, 100),
		remote:       llm.NewRemoteChatContext(),
		audioBStream: newOpenAIRealtimeAudioByteStream(),
	}

	go s.eventLoop()
	s.optionsState = openAIRealtimeOptionEntries(initialSession)
	if err := s.sendMsg(openAIRealtimeInitialSessionUpdateMessage(initialSession)); err != nil {
		_ = s.Close()
		return nil, err
	}
	m.registerSession(s)

	return s, nil
}

func (m *RealtimeModel) registerSession(session *realtimeSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions == nil {
		m.sessions = make(map[*realtimeSession]struct{})
	}
	m.sessions[session] = struct{}{}
}

func (m *RealtimeModel) unregisterSession(session *realtimeSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, session)
}

func openAIRealtimeBaseURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	path := strings.TrimRight(u.Path, "/")
	switch path {
	case "", "/v1", "/openai", "/openai/v1":
		u.Path = path + "/realtime"
	default:
		u.Path = path
	}
	return u.String()
}

func openAIRealtimeSessionURL(baseURL, model string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Sprintf("%s?model=%s", baseURL, url.QueryEscape(model))
	}
	query := u.Query()
	if query.Get("model") == "" {
		query.Set("model", model)
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func defaultOpenAIRealtimeWebsocketDialer(endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	return websocket.DefaultDialer.Dial(endpoint, headers)
}

func openAIRealtimeInitialSession(model string, modalities ...[]string) map[string]any {
	audioFormat := map[string]any{
		"type": "audio/pcm",
		"rate": openAIRealtimeInputSampleRate,
	}
	outputModality := "audio"
	if len(modalities) > 0 && !realtimeModalitiesInclude(modalities[0], "audio") {
		outputModality = "text"
	}
	return map[string]any{
		"type":              "realtime",
		"model":             model,
		"output_modalities": []string{outputModality},
		"audio": map[string]any{
			"input": map[string]any{
				"format": audioFormat,
				"transcription": map[string]any{
					"model": "gpt-4o-mini-transcribe",
				},
				"turn_detection": map[string]any{
					"type":               "semantic_vad",
					"create_response":    true,
					"eagerness":          "medium",
					"interrupt_response": true,
				},
			},
			"output": map[string]any{
				"format": audioFormat,
				"speed":  openAIRealtimeDefaultSpeed,
				"voice":  openAIRealtimeDefaultVoice,
			},
		},
		"max_output_tokens": openAIRealtimeDefaultMaxOutputTokens,
		"tool_choice":       "auto",
	}
}

func openAIRealtimeInitialSessionUpdateMessage(session map[string]any) map[string]any {
	return map[string]any{
		"type":     "session.update",
		"event_id": cavosmath.ShortUUID("session_update_"),
		"session":  session,
	}
}

func (s *realtimeSession) EventCh() <-chan llm.RealtimeEvent {
	return s.eventCh
}

func (s *realtimeSession) UpdateInstructions(instructions string) error {
	msg := map[string]any{
		"type":     "session.update",
		"event_id": cavosmath.ShortUUID("instructions_update_"),
		"session": map[string]any{
			"instructions": instructions,
		},
	}
	if err := s.sendMsg(msg); err != nil {
		return err
	}
	s.mu.Lock()
	s.instructions = instructions
	s.mu.Unlock()
	return nil
}

func (s *realtimeSession) UpdateChatContext(chatCtx *llm.ChatContext) error {
	syncedChatCtx := openAIRealtimeSyncedChatContext(chatCtx)
	var (
		msgs []map[string]any
		err  error
	)
	if s.remote == nil {
		msgs, err = openAIRealtimeChatContextCreateMessages(syncedChatCtx)
	} else {
		msgs, err = openAIRealtimeChatContextUpdateMessages(s.remote.ToChatCtx(), syncedChatCtx)
	}
	if err != nil {
		return err
	}
	for _, msg := range msgs {
		if err := s.sendMsg(msg); err != nil {
			return err
		}
	}
	s.remote = openAIRealtimeRemoteSnapshot(syncedChatCtx)
	return nil
}

func (s *realtimeSession) UpdateTools(tools []llm.Tool) error {
	msg := map[string]any{
		"type":     "session.update",
		"event_id": cavosmath.ShortUUID("tools_update_"),
		"session": map[string]any{
			"tools": openAIRealtimeTools(tools),
		},
	}
	return s.sendMsg(msg)
}

func openAIRealtimeTools(tools []llm.Tool) []map[string]any {
	var oaTools []map[string]any
	for _, t := range tools {
		oaTools = append(oaTools, map[string]any{
			"type":        "function",
			"name":        t.Name(),
			"description": t.Description(),
			"parameters":  t.Parameters(),
		})
	}
	return oaTools
}

func openAIRealtimeChatContextCreateMessages(chatCtx *llm.ChatContext) ([]map[string]any, error) {
	return openAIRealtimeChatContextUpdateMessages(llm.NewChatContext(), openAIRealtimeSyncedChatContext(chatCtx))
}

func openAIRealtimeChatContextUpdateMessages(oldCtx, newCtx *llm.ChatContext) ([]map[string]any, error) {
	if oldCtx == nil {
		oldCtx = llm.NewChatContext()
	}
	if newCtx == nil {
		newCtx = llm.NewChatContext()
	}
	diff := llm.ComputeChatCtxDiff(oldCtx, newCtx)
	msgs := make([]map[string]any, 0, len(diff.ToRemove)+len(diff.ToCreate)+len(diff.ToUpdate)*2)
	for _, itemID := range diff.ToRemove {
		msgs = append(msgs, openAIRealtimeDeleteChatItemMessage(itemID))
	}
	for _, item := range diff.ToCreate {
		msg, err := openAIRealtimeCreateChatItemMessage(newCtx, item[0], item[1])
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}
	for _, item := range diff.ToUpdate {
		if item[1] == nil {
			continue
		}
		msgs = append(msgs, openAIRealtimeDeleteChatItemMessage(*item[1]))
		msg, err := openAIRealtimeCreateChatItemMessage(newCtx, item[0], item[1])
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

func openAIRealtimeSyncedChatContext(chatCtx *llm.ChatContext) *llm.ChatContext {
	if chatCtx == nil {
		return llm.NewChatContext()
	}
	synced := chatCtx.Copy(llm.ChatContextCopyOptions{
		ExcludeHandoff:      true,
		ExcludeConfigUpdate: true,
	})
	items := synced.Items[:0]
	for _, item := range synced.Items {
		if openAIRealtimeInstructionItem(item) {
			continue
		}
		items = append(items, item)
	}
	synced.Items = items
	return synced
}

func openAIRealtimeInstructionItem(item llm.ChatItem) bool {
	msg, ok := item.(*llm.ChatMessage)
	if !ok || (msg.Role != llm.ChatRoleSystem && msg.Role != llm.ChatRoleDeveloper) {
		return false
	}
	for _, content := range msg.Content {
		if content.Instructions != nil {
			return true
		}
	}
	return false
}

func openAIRealtimeRemoteSnapshot(chatCtx *llm.ChatContext) *llm.RemoteChatContext {
	remote := llm.NewRemoteChatContext()
	if chatCtx == nil {
		return remote
	}
	var previous *string
	for _, item := range chatCtx.Items {
		if err := remote.Insert(previous, item); err != nil {
			return remote
		}
		itemID := item.GetID()
		previous = &itemID
	}
	return remote
}

func openAIRealtimeDeleteChatItemMessage(itemID string) map[string]any {
	return map[string]any{
		"type":     "conversation.item.delete",
		"event_id": cavosmath.ShortUUID("chat_ctx_delete_"),
		"item_id":  itemID,
	}
}

func openAIRealtimeCreateChatItemMessage(chatCtx *llm.ChatContext, previousItemID *string, itemID *string) (map[string]any, error) {
	if itemID == nil {
		return nil, fmt.Errorf("missing realtime chat item id")
	}
	item := chatCtx.GetByID(*itemID)
	if item == nil {
		return nil, fmt.Errorf("realtime chat item %q not found", *itemID)
	}
	openAIItem, err := openAIRealtimeChatItemMessage(item)
	if err != nil {
		return nil, err
	}
	previous := any("root")
	if previousItemID != nil {
		previous = *previousItemID
	}
	return map[string]any{
		"type":             "conversation.item.create",
		"event_id":         cavosmath.ShortUUID("chat_ctx_create_"),
		"previous_item_id": previous,
		"item":             openAIItem,
	}, nil
}

func openAIRealtimeChatItemMessage(item llm.ChatItem) (map[string]any, error) {
	switch it := item.(type) {
	case *llm.ChatMessage:
		content, err := openAIRealtimeChatMessageContent(it)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"id":      it.ID,
			"type":    "message",
			"role":    string(openAIRealtimeMessageRole(it.Role)),
			"content": content,
		}, nil
	case *llm.FunctionCall:
		return map[string]any{
			"id":        it.ID,
			"type":      "function_call",
			"call_id":   it.CallID,
			"name":      it.Name,
			"arguments": it.Arguments,
		}, nil
	case *llm.FunctionCallOutput:
		return map[string]any{
			"id":      it.ID,
			"type":    "function_call_output",
			"call_id": it.CallID,
			"output":  it.Output,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported realtime chat item %T", item)
	}
}

func openAIRealtimeMessageRole(role llm.ChatRole) llm.ChatRole {
	if role == llm.ChatRoleDeveloper {
		return llm.ChatRoleSystem
	}
	return role
}

func openAIRealtimeChatMessageContent(msg *llm.ChatMessage) ([]map[string]any, error) {
	content := make([]map[string]any, 0, len(msg.Content))
	for _, part := range msg.Content {
		if part.Text != "" || (part.Image == nil && part.Audio == nil && part.Instructions == nil) {
			partType := "input_text"
			if msg.Role == llm.ChatRoleAssistant {
				partType = "output_text"
			}
			content = append(content, map[string]any{
				"type": partType,
				"text": part.Text,
			})
		}
		if msg.Role == llm.ChatRoleUser && part.Image != nil {
			imagePart, err := openAIRealtimeImageContent(part.Image)
			if err != nil {
				return nil, err
			}
			if imagePart != nil {
				content = append(content, imagePart)
			}
		}
		if msg.Role == llm.ChatRoleUser && part.Audio != nil {
			audioPart := map[string]any{"type": "input_audio"}
			if encoded := openAIRealtimeAudioContent(part.Audio); encoded != "" {
				audioPart["audio"] = encoded
			}
			if part.Audio.Transcript != "" {
				audioPart["transcript"] = part.Audio.Transcript
			}
			if len(audioPart) > 1 {
				content = append(content, audioPart)
			}
		}
	}
	return content, nil
}

func openAIRealtimeAudioContent(audio *llm.AudioContent) string {
	if audio == nil || len(audio.Frames) == 0 {
		return ""
	}
	var data []byte
	for _, frame := range audio.Frames {
		switch f := frame.(type) {
		case *model.AudioFrame:
			if f != nil {
				data = append(data, f.Data...)
			}
		case model.AudioFrame:
			data = append(data, f.Data...)
		}
	}
	if len(data) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(data)
}

func openAIRealtimeImageContent(image *llm.ImageContent) (map[string]any, error) {
	img, err := llm.SerializeImage(image)
	if err != nil {
		return nil, err
	}
	if img.ExternalURL != "" {
		return nil, nil
	}
	return map[string]any{
		"type":      "input_image",
		"image_url": fmt.Sprintf("data:%s;base64,%s", img.MIMEType, base64.StdEncoding.EncodeToString(img.DataBytes)),
	}, nil
}

func (s *realtimeSession) UpdateOptions(options llm.RealtimeSessionOptions) error {
	msg := openAIRealtimeUpdateOptionsMessageWithEventID(options, cavosmath.ShortUUID("options_update_"))
	if msg == nil {
		return nil
	}
	session, _ := msg["session"].(map[string]any)
	s.mu.Lock()
	changedSession, changedOptions := openAIRealtimeChangedOptionsSession(session, s.optionsState)
	if len(changedSession) == 0 {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	msg["session"] = changedSession
	if err := s.sendMsg(msg); err != nil {
		return err
	}
	s.mu.Lock()
	if s.optionsState == nil {
		s.optionsState = make(map[string]any, len(changedOptions))
	}
	for key, value := range changedOptions {
		s.optionsState[key] = value
	}
	s.mu.Unlock()
	return nil
}

func openAIRealtimeUpdateOptionsMessage(options llm.RealtimeSessionOptions) map[string]any {
	return openAIRealtimeUpdateOptionsMessageWithEventID(options, "")
}

func openAIRealtimeUpdateOptionsMessageWithEventID(options llm.RealtimeSessionOptions, eventID string) map[string]any {
	session := make(map[string]any)
	if toolChoice := openAIRealtimeToolChoice(options.ToolChoice); toolChoice != nil {
		session["tool_choice"] = toolChoice
	} else if options.ToolChoiceSet {
		session["tool_choice"] = "auto"
	}
	if options.MaxResponseOutputTokensSet || options.MaxResponseOutputTokens != nil {
		session["max_output_tokens"] = options.MaxResponseOutputTokens
	}
	if options.TruncationSet || options.Truncation != nil {
		session["truncation"] = options.Truncation
	}
	if options.TracingSet || options.Tracing != nil {
		session["tracing"] = options.Tracing
	}
	input := make(map[string]any)
	if options.TurnDetectionSet || options.TurnDetection != nil {
		input["turn_detection"] = options.TurnDetection
	}
	if options.InputAudioTranscriptionSet || options.InputAudioTranscription != nil {
		input["transcription"] = options.InputAudioTranscription
	}
	if options.InputAudioNoiseReductionSet || options.InputAudioNoiseReduction != nil {
		input["noise_reduction"] = options.InputAudioNoiseReduction
	}
	output := make(map[string]any)
	if options.Voice != "" {
		output["voice"] = options.Voice
	}
	if options.SpeedSet || options.Speed > 0 {
		output["speed"] = options.Speed
	}
	if len(input) > 0 || len(output) > 0 {
		audio := make(map[string]any)
		if len(input) > 0 {
			audio["input"] = input
		}
		if len(output) > 0 {
			audio["output"] = output
		}
		session["audio"] = audio
	}
	if len(session) == 0 {
		return nil
	}
	msg := map[string]any{
		"type":    "session.update",
		"session": session,
	}
	if eventID != "" {
		msg["event_id"] = eventID
	}
	return msg
}

func openAIRealtimeChangedOptionsSession(session map[string]any, current map[string]any) (map[string]any, map[string]any) {
	options := openAIRealtimeOptionEntries(session)
	changedOptions := make(map[string]any)
	for key, value := range options {
		if current != nil && reflect.DeepEqual(current[key], value) {
			continue
		}
		changedOptions[key] = value
	}
	return openAIRealtimeSessionFromOptionEntries(changedOptions), changedOptions
}

func openAIRealtimeMergeSessionOptions(dst *llm.RealtimeSessionOptions, src llm.RealtimeSessionOptions) {
	if src.ToolChoiceSet || src.ToolChoice != nil {
		dst.ToolChoice = src.ToolChoice
		dst.ToolChoiceSet = true
	}
	if src.MaxResponseOutputTokensSet || src.MaxResponseOutputTokens != nil {
		dst.MaxResponseOutputTokens = src.MaxResponseOutputTokens
		dst.MaxResponseOutputTokensSet = true
	}
	if src.TruncationSet || src.Truncation != nil {
		dst.Truncation = src.Truncation
		dst.TruncationSet = true
	}
	if src.TracingSet || src.Tracing != nil {
		dst.Tracing = src.Tracing
		dst.TracingSet = true
	}
	if src.TurnDetectionSet || src.TurnDetection != nil {
		dst.TurnDetection = src.TurnDetection
		dst.TurnDetectionSet = true
	}
	if src.InputAudioTranscriptionSet || src.InputAudioTranscription != nil {
		dst.InputAudioTranscription = src.InputAudioTranscription
		dst.InputAudioTranscriptionSet = true
	}
	if src.InputAudioNoiseReductionSet || src.InputAudioNoiseReduction != nil {
		dst.InputAudioNoiseReduction = src.InputAudioNoiseReduction
		dst.InputAudioNoiseReductionSet = true
	}
	if src.Voice != "" {
		dst.Voice = src.Voice
	}
	if src.SpeedSet || src.Speed > 0 {
		dst.Speed = src.Speed
		dst.SpeedSet = true
	}
}

func openAIRealtimeMergeSessionPayload(dst map[string]any, src map[string]any) {
	for key, value := range src {
		if key != "audio" {
			dst[key] = value
			continue
		}
		dstAudio, _ := dst["audio"].(map[string]any)
		if dstAudio == nil {
			dstAudio = make(map[string]any)
			dst["audio"] = dstAudio
		}
		srcAudio, _ := value.(map[string]any)
		for audioKey, audioValue := range srcAudio {
			dstConfig, _ := dstAudio[audioKey].(map[string]any)
			if dstConfig == nil {
				dstConfig = make(map[string]any)
				dstAudio[audioKey] = dstConfig
			}
			srcConfig, _ := audioValue.(map[string]any)
			for configKey, configValue := range srcConfig {
				dstConfig[configKey] = configValue
			}
		}
	}
}

func openAIRealtimeOptionEntries(session map[string]any) map[string]any {
	entries := make(map[string]any)
	if value, ok := session["tool_choice"]; ok {
		entries["tool_choice"] = value
	}
	if value, ok := session["max_output_tokens"]; ok {
		entries["max_output_tokens"] = value
	}
	if value, ok := session["truncation"]; ok {
		entries["truncation"] = value
	}
	if value, ok := session["tracing"]; ok {
		entries["tracing"] = value
	}
	audio, _ := session["audio"].(map[string]any)
	input, _ := audio["input"].(map[string]any)
	if value, ok := input["turn_detection"]; ok {
		entries["audio.input.turn_detection"] = value
	}
	if value, ok := input["transcription"]; ok {
		entries["audio.input.transcription"] = value
	}
	if value, ok := input["noise_reduction"]; ok {
		entries["audio.input.noise_reduction"] = value
	}
	output, _ := audio["output"].(map[string]any)
	if value, ok := output["voice"]; ok {
		entries["audio.output.voice"] = value
	}
	if value, ok := output["speed"]; ok {
		entries["audio.output.speed"] = value
	}
	return entries
}

func openAIRealtimeSessionFromOptionEntries(entries map[string]any) map[string]any {
	session := make(map[string]any)
	input := make(map[string]any)
	output := make(map[string]any)
	for key, value := range entries {
		switch key {
		case "tool_choice", "max_output_tokens", "truncation", "tracing":
			session[key] = value
		case "audio.input.turn_detection":
			input["turn_detection"] = value
		case "audio.input.transcription":
			input["transcription"] = value
		case "audio.input.noise_reduction":
			input["noise_reduction"] = value
		case "audio.output.voice":
			output["voice"] = value
		case "audio.output.speed":
			output["speed"] = value
		}
	}
	if len(input) > 0 || len(output) > 0 {
		audio := make(map[string]any)
		if len(input) > 0 {
			audio["input"] = input
		}
		if len(output) > 0 {
			audio["output"] = output
		}
		session["audio"] = audio
	}
	return session
}

func openAIRealtimeToolChoice(choice llm.ToolChoice) any {
	switch tc := choice.(type) {
	case nil:
		return nil
	case string:
		return tc
	case map[string]any:
		if tc["type"] != "function" {
			return nil
		}
		function, ok := tc["function"].(map[string]any)
		if !ok {
			return nil
		}
		name, ok := function["name"].(string)
		if !ok || name == "" {
			return nil
		}
		return map[string]any{
			"type": "function",
			"name": name,
		}
	default:
		return nil
	}
}

func (s *realtimeSession) Interrupt() error {
	s.mu.Lock()
	active := s.generation != nil || s.pendingResponses > 0
	s.mu.Unlock()
	if !active {
		return nil
	}
	msg := map[string]any{
		"type": "response.cancel",
	}
	return s.sendMsg(msg)
}

func (s *realtimeSession) PushAudio(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	frame, err := normalizeOpenAIRealtimeInputAudio(frame)
	if err != nil {
		return err
	}
	if s.audioBStream == nil {
		s.audioBStream = newOpenAIRealtimeAudioByteStream()
	}

	for _, chunk := range s.audioBStream.Write(frame.Data) {
		msg := map[string]any{
			"type":  "input_audio_buffer.append",
			"audio": base64.StdEncoding.EncodeToString(chunk.Data),
		}
		if err := s.sendMsg(msg); err != nil {
			return err
		}
		if chunk.SampleRate > 0 {
			s.pushedDuration += float64(chunk.SamplesPerChannel) / float64(chunk.SampleRate)
		}
	}
	return nil
}

func normalizeOpenAIRealtimeInputAudio(frame *model.AudioFrame) (*model.AudioFrame, error) {
	if frame == nil {
		return nil, nil
	}
	if frame.SampleRate == 0 || frame.NumChannels == 0 {
		return frame, nil
	}
	if frame.SampleRate == openAIRealtimeInputSampleRate && frame.NumChannels == openAIRealtimeInputNumChannels {
		return frame, nil
	}
	if len(frame.Data)%2 != 0 {
		return nil, fmt.Errorf("openai realtime input audio must be 16-bit PCM")
	}
	samplesPerChannel := frame.SamplesPerChannel
	if samplesPerChannel == 0 {
		samplesPerChannel = uint32(len(frame.Data)) / frame.NumChannels / 2
	}
	expectedBytes := int(samplesPerChannel * frame.NumChannels * 2)
	if len(frame.Data) < expectedBytes {
		return nil, fmt.Errorf("openai realtime input audio data is shorter than declared sample count")
	}
	if samplesPerChannel == 0 {
		return &model.AudioFrame{
			SampleRate:        openAIRealtimeInputSampleRate,
			NumChannels:       openAIRealtimeInputNumChannels,
			SamplesPerChannel: 0,
		}, nil
	}

	outSamples := uint32((uint64(samplesPerChannel)*uint64(openAIRealtimeInputSampleRate) + uint64(frame.SampleRate) - 1) / uint64(frame.SampleRate))
	out := make([]byte, int(outSamples*openAIRealtimeInputNumChannels*2))
	channelCount := int(frame.NumChannels)
	for outIdx := uint32(0); outIdx < outSamples; outIdx++ {
		srcIdx := uint32(uint64(outIdx) * uint64(frame.SampleRate) / uint64(openAIRealtimeInputSampleRate))
		if srcIdx >= samplesPerChannel {
			srcIdx = samplesPerChannel - 1
		}
		var sum int32
		for ch := 0; ch < channelCount; ch++ {
			offset := (int(srcIdx)*channelCount + ch) * 2
			sum += int32(int16(binary.LittleEndian.Uint16(frame.Data[offset : offset+2])))
		}
		sample := sum / int32(channelCount)
		binary.LittleEndian.PutUint16(out[int(outIdx)*2:int(outIdx)*2+2], uint16(int16(sample)))
	}

	return &model.AudioFrame{
		Data:              out,
		SampleRate:        openAIRealtimeInputSampleRate,
		NumChannels:       openAIRealtimeInputNumChannels,
		SamplesPerChannel: outSamples,
	}, nil
}

func newOpenAIRealtimeAudioByteStream() *audio.AudioByteStream {
	return audio.NewAudioByteStream(
		openAIRealtimeInputSampleRate,
		openAIRealtimeInputNumChannels,
		openAIRealtimeInputSampleRate/10,
	)
}

func (s *realtimeSession) PushVideo(frame *images.VideoFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	data, err := images.Encode(frame, images.NewEncodeOptions())
	if err != nil {
		return err
	}
	image := &llm.ImageContent{
		Image:    fmt.Sprintf("data:image/jpeg;base64,%s", base64.StdEncoding.EncodeToString(data)),
		MimeType: "image/jpeg",
	}
	msg, err := openAIRealtimeVideoMessage(image)
	if err != nil {
		return err
	}
	return s.sendMsg(msg)
}

func openAIRealtimeVideoMessage(image *llm.ImageContent) (map[string]any, error) {
	img, err := llm.SerializeImage(image)
	if err != nil {
		return nil, err
	}
	if img.ExternalURL != "" {
		return nil, fmt.Errorf("openai realtime input_image does not support external image URLs")
	}
	url := fmt.Sprintf("data:%s;base64,%s", img.MIMEType, base64.StdEncoding.EncodeToString(img.DataBytes))
	return map[string]any{
		"type":     "conversation.item.create",
		"event_id": cavosmath.ShortUUID("video_"),
		"item": map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{
					"type":      "input_image",
					"image_url": url,
				},
			},
		},
	}, nil
}

func (s *realtimeSession) GenerateReply(options llm.RealtimeGenerateReplyOptions) error {
	s.mu.Lock()
	instructions := s.instructions
	s.mu.Unlock()
	if err := s.sendMsg(openAIRealtimeGenerateReplyMessageWithSessionInstructions(options, instructions)); err != nil {
		return err
	}
	s.mu.Lock()
	s.pendingResponses++
	s.mu.Unlock()
	return nil
}

func (s *realtimeSession) Say(text string) error {
	return llm.NewRealtimeError("openai realtime session does not support say", nil)
}

func openAIRealtimeGenerateReplyMessage(options llm.RealtimeGenerateReplyOptions) map[string]any {
	return openAIRealtimeGenerateReplyMessageWithSessionInstructions(options, "")
}

func openAIRealtimeGenerateReplyMessageWithSessionInstructions(options llm.RealtimeGenerateReplyOptions, sessionInstructions string) map[string]any {
	response := make(map[string]any)
	eventID := cavosmath.ShortUUID("response_create_")
	response["metadata"] = map[string]any{"client_event_id": eventID}
	if sessionInstructions != "" && options.Instructions != "" {
		options.Instructions = sessionInstructions + "\n" + options.Instructions
	}
	if options.Instructions != "" {
		response["instructions"] = options.Instructions
	}
	if toolChoice := openAIRealtimeToolChoice(options.ToolChoice); toolChoice != nil {
		response["tool_choice"] = toolChoice
	}
	if options.Tools != nil {
		response["tools"] = openAIRealtimeTools(options.Tools)
	}

	return map[string]any{
		"type":     "response.create",
		"event_id": eventID,
		"response": response,
	}
}

func (s *realtimeSession) Truncate(options llm.RealtimeTruncateOptions) error {
	if realtimeModalitiesInclude(options.Modalities, "audio") {
		return s.sendMsg(openAIRealtimeTruncateMessage(options))
	}
	if options.AudioTranscript == nil {
		return nil
	}
	if s.remote == nil {
		s.remote = llm.NewRemoteChatContext()
	}
	msgs, err := openAIRealtimeTruncateTranscriptUpdateMessages(s.remote.ToChatCtx(), options)
	if err != nil {
		return err
	}
	for _, msg := range msgs {
		if err := s.sendMsg(msg); err != nil {
			return err
		}
	}
	s.remote = openAIRealtimeRemoteSnapshot(openAIRealtimeTruncatedTranscriptChatContext(s.remote.ToChatCtx(), options))
	return nil
}

func openAIRealtimeTruncateMessage(options llm.RealtimeTruncateOptions) map[string]any {
	return map[string]any{
		"type":          "conversation.item.truncate",
		"content_index": 0,
		"item_id":       options.MessageID,
		"audio_end_ms":  options.AudioEndMillis,
	}
}

func realtimeModalitiesInclude(modalities []string, target string) bool {
	for _, modality := range modalities {
		if modality == target {
			return true
		}
	}
	return false
}

func openAIRealtimeTruncateTranscriptUpdateMessages(oldCtx *llm.ChatContext, options llm.RealtimeTruncateOptions) ([]map[string]any, error) {
	newCtx := openAIRealtimeTruncatedTranscriptChatContext(oldCtx, options)
	return openAIRealtimeChatContextUpdateMessages(oldCtx, newCtx)
}

func openAIRealtimeTruncatedTranscriptChatContext(oldCtx *llm.ChatContext, options llm.RealtimeTruncateOptions) *llm.ChatContext {
	newCtx := openAIRealtimeSyncedChatContext(oldCtx)
	if options.AudioTranscript == nil {
		return newCtx
	}
	idx := newCtx.IndexByID(options.MessageID)
	if idx == nil {
		return newCtx
	}
	msg, ok := newCtx.Items[*idx].(*llm.ChatMessage)
	if !ok {
		return newCtx
	}
	updated := *msg
	updated.Content = []llm.ChatContent{{Text: *options.AudioTranscript}}
	newCtx.Items[*idx] = &updated
	return newCtx
}

func (s *realtimeSession) CommitAudio() error {
	if s.pushedDuration <= 0.1 {
		return nil
	}
	if err := s.sendMsg(openAIRealtimeCommitAudioMessage()); err != nil {
		return err
	}
	s.pushedDuration = 0
	return nil
}

func openAIRealtimeCommitAudioMessage() map[string]any {
	return map[string]any{
		"type": "input_audio_buffer.commit",
	}
}

func (s *realtimeSession) ClearAudio() error {
	if err := s.sendMsg(openAIRealtimeClearAudioMessage()); err != nil {
		return err
	}
	s.pushedDuration = 0
	return nil
}

func openAIRealtimeClearAudioMessage() map[string]any {
	return map[string]any{
		"type": "input_audio_buffer.clear",
	}
}

func (s *realtimeSession) Close() error {
	if s.model != nil {
		s.model.unregisterSession(s)
	}
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.Close()
}

func (s *realtimeSession) sendMsg(msg any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, b)
}

func (s *realtimeSession) eventLoop() {
	defer close(s.eventCh)
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			_, msg, err := s.conn.ReadMessage()
			if err != nil {
				logger.Logger.Errorw("OpenAI realtime disconnected", err)
				s.cancel()
				return
			}

			var ev map[string]any
			if err := json.Unmarshal(msg, &ev); err != nil {
				continue
			}

			trackedEvent, trackedOK := s.trackOpenAIRealtimeEvent(ev)
			realtimeEvent, ok := openAIRealtimeEvent(ev)
			if !ok {
				if trackedOK {
					s.eventCh <- trackedEvent
				}
				continue
			}
			if realtimeEvent.Type == llm.RealtimeEventTypeError {
				logger.Logger.Errorw("OpenAI realtime error", nil, "payload", string(msg))
			}
			if realtimeEvent.Type == llm.RealtimeEventTypeSpeechStopped && realtimeEvent.SpeechStopped != nil {
				realtimeEvent.SpeechStopped.UserTranscriptionEnabled = s.inputAudioTranscriptionEnabled()
			}
			realtimeEvent = s.trackRealtimeEvent(realtimeEvent)
			s.eventCh <- realtimeEvent
		}
	}
}

func (s *realtimeSession) inputAudioTranscriptionEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.optionsState["audio.input.transcription"]
	return ok && value != nil
}

func (s *realtimeSession) trackOpenAIRealtimeEvent(ev map[string]any) (llm.RealtimeEvent, bool) {
	evType, _ := ev["type"].(string)
	switch evType {
	case "response.output_item.added":
		item, _ := ev["item"].(map[string]any)
		itemID, _ := item["id"].(string)
		itemType, _ := item["type"].(string)
		if itemID == "" || itemType != "message" || s.generation == nil {
			return llm.RealtimeEvent{}, false
		}
		msg := &realtimeMessageGeneration{
			textCh:       make(chan string, 100),
			audioCh:      make(chan *model.AudioFrame, 100),
			modalitiesCh: make(chan []string, 1),
		}
		s.generation.messages[itemID] = msg
		message := llm.MessageGeneration{
			MessageID:    itemID,
			TextCh:       msg.textCh,
			AudioCh:      msg.audioCh,
			ModalitiesCh: msg.modalitiesCh,
		}
		select {
		case s.generation.messageCh <- message:
		default:
			logger.Logger.Warnw("dropping OpenAI realtime message generation for full stream", nil, "item_id", itemID)
		}
	case "response.content_part.added":
		itemID, _ := ev["item_id"].(string)
		part, _ := ev["part"].(map[string]any)
		partType, _ := part["type"].(string)
		if itemID == "" || partType == "" {
			return llm.RealtimeEvent{}, false
		}
		if partType == "text" {
			s.setRealtimeMessageModalities(itemID, []string{"text"})
		} else {
			s.setRealtimeMessageModalities(itemID, []string{"audio", "text"})
		}
	case "response.output_item.done":
		item, _ := ev["item"].(map[string]any)
		itemID, _ := item["id"].(string)
		itemType, _ := item["type"].(string)
		if itemID == "" || s.generation == nil {
			return llm.RealtimeEvent{}, false
		}
		switch itemType {
		case "message":
			s.closeRealtimeMessageStreams(s.generation.messages[itemID])
		case "function_call":
			call, err := openAIRealtimeFunctionCall(item)
			if err != nil {
				return llm.RealtimeEvent{}, false
			}
			select {
			case s.generation.functionCh <- call:
			default:
				logger.Logger.Warnw("dropping OpenAI realtime function call for full stream", nil, "item_id", itemID)
			}
		}
	case "response.done":
		s.closeRealtimeGeneration()
	case "conversation.item.deleted":
		itemID, _ := ev["item_id"].(string)
		if itemID == "" {
			return llm.RealtimeEvent{}, false
		}
		if s.remote == nil {
			s.remote = llm.NewRemoteChatContext()
		}
		if err := s.remote.Delete(itemID); err != nil {
			logger.Logger.Warnw("failed to track OpenAI realtime deleted item", err, "item_id", itemID)
		}
		s.clearRealtimeInputTranscripts(itemID)
	case "conversation.item.input_audio_transcription.failed":
		itemID, _ := ev["item_id"].(string)
		contentIndex := openAIRealtimeInt(ev["content_index"])
		logger.Logger.Errorw("OpenAI realtime input audio transcription failed", nil, "item_id", itemID, "error", ev["error"])
		partial, ok := s.clearRealtimeInputTranscript(itemID, contentIndex)
		if !ok {
			return llm.RealtimeEvent{}, false
		}
		return llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
			InputTranscription: &llm.InputTranscriptionCompleted{
				ItemID:       itemID,
				ContentIndex: contentIndex,
				Transcript:   partial,
				IsFinal:      true,
			},
		}, true
	}
	return llm.RealtimeEvent{}, false
}

func (s *realtimeSession) trackRealtimeEvent(ev llm.RealtimeEvent) llm.RealtimeEvent {
	if ev.Type == llm.RealtimeEventTypeGenerationCreated {
		return s.trackRealtimeGenerationCreated(ev)
	}
	if ev.Type == llm.RealtimeEventTypeText {
		s.trackRealtimeText(ev)
		return ev
	}
	if ev.Type == llm.RealtimeEventTypeAudio {
		s.trackRealtimeAudio(ev)
		return ev
	}
	if ev.Type == llm.RealtimeEventTypeRemoteItemAdded {
		s.trackRealtimeRemoteItemAdded(ev)
		return ev
	}
	if ev.Type == llm.RealtimeEventTypeInputAudioTranscriptionCompleted {
		return s.trackRealtimeInputTranscription(ev)
	}
	return ev
}

func (s *realtimeSession) trackRealtimeGenerationCreated(ev llm.RealtimeEvent) llm.RealtimeEvent {
	if ev.Generation == nil {
		return ev
	}
	if s.pendingResponses > 0 {
		s.pendingResponses--
	}
	generation := &realtimeGeneration{
		messageCh:  make(chan llm.MessageGeneration, 100),
		functionCh: make(chan *llm.FunctionCall, 100),
		messages:   make(map[string]*realtimeMessageGeneration),
	}
	s.closeRealtimeGeneration()
	s.generation = generation
	ev.Generation.MessageCh = generation.messageCh
	ev.Generation.FunctionCh = generation.functionCh
	return ev
}

func (s *realtimeSession) trackRealtimeText(ev llm.RealtimeEvent) {
	if s.generation == nil || ev.ItemID == "" {
		return
	}
	msg := s.generation.messages[ev.ItemID]
	if msg == nil {
		return
	}
	select {
	case msg.textCh <- ev.Text:
	default:
		logger.Logger.Warnw("dropping OpenAI realtime text delta for full message stream", nil, "item_id", ev.ItemID)
	}
}

func (s *realtimeSession) trackRealtimeAudio(ev llm.RealtimeEvent) {
	if s.generation == nil || ev.ItemID == "" {
		return
	}
	msg := s.generation.messages[ev.ItemID]
	if msg == nil {
		return
	}
	frame := &model.AudioFrame{
		Data:              ev.Data,
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(ev.Data) / 2),
	}
	s.setRealtimeMessageModalities(ev.ItemID, []string{"audio", "text"})
	select {
	case msg.audioCh <- frame:
	default:
		logger.Logger.Warnw("dropping OpenAI realtime audio delta for full message stream", nil, "item_id", ev.ItemID)
	}
}

func (s *realtimeSession) setRealtimeMessageModalities(itemID string, modalities []string) {
	if s.generation == nil || itemID == "" {
		return
	}
	msg := s.generation.messages[itemID]
	if msg == nil || msg.modalities != nil {
		return
	}
	msg.modalities = append([]string(nil), modalities...)
	select {
	case msg.modalitiesCh <- msg.modalities:
	default:
		logger.Logger.Warnw("dropping OpenAI realtime message modalities for full stream", nil, "item_id", itemID)
	}
}

func (s *realtimeSession) closeRealtimeGeneration() {
	if s.generation == nil {
		return
	}
	for _, msg := range s.generation.messages {
		if msg.modalities == nil {
			msg.modalities = []string{"audio", "text"}
			select {
			case msg.modalitiesCh <- msg.modalities:
			default:
			}
		}
		s.closeRealtimeMessageStreams(msg)
		close(msg.modalitiesCh)
	}
	close(s.generation.messageCh)
	close(s.generation.functionCh)
	s.generation = nil
}

func (s *realtimeSession) closeRealtimeMessageStreams(msg *realtimeMessageGeneration) {
	if msg == nil || msg.streamsClosed {
		return
	}
	close(msg.textCh)
	close(msg.audioCh)
	msg.streamsClosed = true
}

func (s *realtimeSession) trackRealtimeRemoteItemAdded(ev llm.RealtimeEvent) {
	if ev.RemoteItem == nil || ev.RemoteItem.Item == nil {
		return
	}
	if s.remote == nil {
		s.remote = llm.NewRemoteChatContext()
	}
	var previousItemID *string
	if ev.RemoteItem.PreviousItemID != "" {
		previousItemID = &ev.RemoteItem.PreviousItemID
	}
	if err := s.remote.Insert(previousItemID, ev.RemoteItem.Item); err != nil {
		logger.Logger.Warnw("failed to track OpenAI realtime remote item", err, "item_id", ev.RemoteItem.Item.GetID())
	}
}

func (s *realtimeSession) trackRealtimeInputTranscription(ev llm.RealtimeEvent) llm.RealtimeEvent {
	transcription := ev.InputTranscription
	if transcription == nil || transcription.ItemID == "" {
		return ev
	}
	if !transcription.IsFinal {
		if s.inputTranscripts == nil {
			s.inputTranscripts = utils.NewBoundedDict[inputTranscriptKey, realtimeInputTranscript](maxRealtimeInputTranscripts)
		}
		key := inputTranscriptKey{itemID: transcription.ItemID, contentIndex: transcription.ContentIndex}
		accumulated := s.inputTranscripts.SetOrUpdate(key, func() realtimeInputTranscript {
			return realtimeInputTranscript{itemID: transcription.ItemID, contentIndex: transcription.ContentIndex}
		}, func(value realtimeInputTranscript) realtimeInputTranscript {
			value.transcript += transcription.Transcript
			return value
		})
		transcription.Transcript = accumulated.transcript
		return ev
	}
	s.clearRealtimeInputTranscript(transcription.ItemID, transcription.ContentIndex)
	if s.remote == nil {
		return ev
	}
	msg, ok := s.remote.Get(transcription.ItemID).(*llm.ChatMessage)
	if !ok {
		return ev
	}
	if transcription.Transcript != "" {
		msg.Content = append(msg.Content, llm.ChatContent{Text: transcription.Transcript})
	}
	msg.TranscriptConfidence = transcription.Confidence
	return ev
}

func (s *realtimeSession) clearRealtimeInputTranscript(itemID string, contentIndex int) (string, bool) {
	if itemID == "" || s.inputTranscripts == nil {
		return "", false
	}
	_, partial, ok := s.inputTranscripts.PopIf(func(value realtimeInputTranscript) bool {
		return value.itemID == itemID && value.contentIndex == contentIndex
	})
	return partial.transcript, ok
}

func (s *realtimeSession) clearRealtimeInputTranscripts(itemID string) {
	if itemID == "" || s.inputTranscripts == nil {
		return
	}
	for {
		_, _, ok := s.inputTranscripts.PopIf(func(value realtimeInputTranscript) bool {
			return value.itemID == itemID
		})
		if !ok {
			return
		}
	}
}

func openAIRealtimeEvent(ev map[string]any) (llm.RealtimeEvent, bool) {
	evType, _ := ev["type"].(string)
	switch evType {
	case "error":
		return llm.RealtimeEvent{
			Type:  llm.RealtimeEventTypeError,
			Error: fmt.Errorf("openai error: %v", ev),
		}, true
	case "response.output_text.delta", "response.text.delta":
		if delta, ok := ev["delta"].(string); ok {
			return llm.RealtimeEvent{
				Type:         llm.RealtimeEventTypeText,
				ItemID:       openAIRealtimeString(ev["item_id"]),
				ContentIndex: openAIRealtimeInt(ev["content_index"]),
				Text:         delta,
			}, true
		}
	case "response.output_audio_transcript.delta", "response.audio_transcript.delta":
		if delta, ok := ev["delta"].(string); ok {
			return llm.RealtimeEvent{
				Type:         llm.RealtimeEventTypeText,
				ItemID:       openAIRealtimeString(ev["item_id"]),
				ContentIndex: openAIRealtimeInt(ev["content_index"]),
				Text:         delta,
			}, true
		}
	case "response.output_audio.delta", "response.audio.delta":
		if delta, ok := ev["delta"].(string); ok {
			data, err := base64.StdEncoding.DecodeString(delta)
			if err != nil {
				return llm.RealtimeEvent{}, false
			}
			return llm.RealtimeEvent{
				Type:         llm.RealtimeEventTypeAudio,
				ItemID:       openAIRealtimeString(ev["item_id"]),
				ContentIndex: openAIRealtimeInt(ev["content_index"]),
				Data:         data,
			}, true
		}
	case "response.function_call_arguments.delta":
		if name, ok := ev["name"].(string); ok {
			if args, ok2 := ev["delta"].(string); ok2 {
				callID, _ := ev["call_id"].(string)
				return llm.RealtimeEvent{
					Type: llm.RealtimeEventTypeFunctionCall,
					Function: &llm.FunctionToolCall{
						CallID:    callID,
						Name:      name,
						Arguments: args,
					},
				}, true
			}
		}
	case "response.output_item.done":
		item, _ := ev["item"].(map[string]any)
		if itemType, _ := item["type"].(string); itemType != "function_call" {
			return llm.RealtimeEvent{}, false
		}
		call, err := openAIRealtimeFunctionCall(item)
		if err != nil {
			return llm.RealtimeEvent{}, false
		}
		return llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeFunctionCall,
			Function: &llm.FunctionToolCall{
				CallID:    call.CallID,
				Name:      call.Name,
				Arguments: call.Arguments,
			},
		}, true
	case "conversation.item.input_audio_transcription.completed":
		itemID, _ := ev["item_id"].(string)
		contentIndex := openAIRealtimeInt(ev["content_index"])
		transcript, _ := ev["transcript"].(string)
		if itemID == "" && transcript == "" {
			return llm.RealtimeEvent{}, false
		}
		return llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
			InputTranscription: &llm.InputTranscriptionCompleted{
				ItemID:       itemID,
				ContentIndex: contentIndex,
				Transcript:   transcript,
				IsFinal:      true,
				Confidence:   openAIRealtimeConfidenceFromLogprobs(ev["logprobs"]),
			},
		}, true
	case "conversation.item.input_audio_transcription.delta":
		itemID, _ := ev["item_id"].(string)
		contentIndex := openAIRealtimeInt(ev["content_index"])
		delta, _ := ev["delta"].(string)
		if itemID == "" && delta == "" {
			return llm.RealtimeEvent{}, false
		}
		return llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
			InputTranscription: &llm.InputTranscriptionCompleted{
				ItemID:       itemID,
				ContentIndex: contentIndex,
				Transcript:   delta,
				IsFinal:      false,
			},
		}, true
	case "response.created":
		response, _ := ev["response"].(map[string]any)
		responseID, _ := response["id"].(string)
		if responseID == "" {
			return llm.RealtimeEvent{}, false
		}
		_, userInitiated := openAIRealtimeResponseClientEventID(response)
		return llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeGenerationCreated,
			Generation: &llm.GenerationCreatedEvent{
				ResponseID:    responseID,
				UserInitiated: userInitiated,
			},
		}, true
	case "conversation.item.added", "conversation.item.created":
		item, ok := ev["item"].(map[string]any)
		if !ok {
			return llm.RealtimeEvent{}, false
		}
		chatItem, err := openAIRealtimeChatItem(item)
		if err != nil {
			return llm.RealtimeEvent{}, false
		}
		previousItemID, _ := ev["previous_item_id"].(string)
		return llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeRemoteItemAdded,
			RemoteItem: &llm.RemoteItemAddedEvent{
				PreviousItemID: previousItemID,
				Item:           chatItem,
			},
		}, true
	case "response.done":
		response, _ := ev["response"].(map[string]any)
		metrics, ok := openAIRealtimeMetrics(response)
		if !ok {
			return llm.RealtimeEvent{}, false
		}
		return llm.RealtimeEvent{
			Type:    llm.RealtimeEventTypeMetricsCollected,
			Metrics: metrics,
		}, true
	case "input_audio_buffer.speech_started":
		return llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStarted}, true
	case "input_audio_buffer.speech_stopped":
		return llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeSpeechStopped,
			SpeechStopped: &llm.InputSpeechStoppedEvent{
				UserTranscriptionEnabled: true,
			},
		}, true
	}
	return llm.RealtimeEvent{}, false
}

func openAIRealtimeMetrics(response map[string]any) (*telemetry.RealtimeModelMetrics, bool) {
	requestID, _ := response["id"].(string)
	if requestID == "" {
		return nil, false
	}
	usage, _ := response["usage"].(map[string]any)
	inputDetails, _ := usage["input_token_details"].(map[string]any)
	outputDetails, _ := usage["output_token_details"].(map[string]any)
	cachedDetails, _ := inputDetails["cached_tokens_details"].(map[string]any)
	status, _ := response["status"].(string)
	return &telemetry.RealtimeModelMetrics{
		RequestID:       requestID,
		Cancelled:       status == "cancelled",
		InputTokens:     openAIRealtimeInt(usage["input_tokens"]),
		OutputTokens:    openAIRealtimeInt(usage["output_tokens"]),
		TotalTokens:     openAIRealtimeInt(usage["total_tokens"]),
		TokensPerSecond: openAIRealtimeFloat(usage["tokens_per_second"]),
		InputTokenDetails: telemetry.InputTokenDetails{
			AudioTokens:  openAIRealtimeInt(inputDetails["audio_tokens"]),
			TextTokens:   openAIRealtimeInt(inputDetails["text_tokens"]),
			ImageTokens:  openAIRealtimeInt(inputDetails["image_tokens"]),
			CachedTokens: openAIRealtimeInt(inputDetails["cached_tokens"]),
			CachedTokensDetails: &telemetry.CachedTokenDetails{
				TextTokens:  openAIRealtimeInt(cachedDetails["text_tokens"]),
				AudioTokens: openAIRealtimeInt(cachedDetails["audio_tokens"]),
				ImageTokens: openAIRealtimeInt(cachedDetails["image_tokens"]),
			},
		},
		OutputTokenDetails: telemetry.OutputTokenDetails{
			TextTokens:  openAIRealtimeInt(outputDetails["text_tokens"]),
			AudioTokens: openAIRealtimeInt(outputDetails["audio_tokens"]),
			ImageTokens: openAIRealtimeInt(outputDetails["image_tokens"]),
		},
	}, true
}

func openAIRealtimeChatItem(item map[string]any) (llm.ChatItem, error) {
	itemType, _ := item["type"].(string)
	switch itemType {
	case "message":
		return openAIRealtimeChatMessage(item)
	case "function_call":
		return openAIRealtimeFunctionCall(item)
	case "function_call_output":
		return openAIRealtimeFunctionCallOutput(item)
	default:
		return nil, fmt.Errorf("unsupported item type: %s", itemType)
	}
}

func openAIRealtimeFunctionCallOutput(item map[string]any) (*llm.FunctionCallOutput, error) {
	id, _ := item["id"].(string)
	callID, _ := item["call_id"].(string)
	output, ok := item["output"].(string)
	if id == "" || callID == "" {
		return nil, fmt.Errorf("malformed realtime function call output item")
	}
	if !ok {
		return nil, fmt.Errorf("output is None")
	}
	return &llm.FunctionCallOutput{
		ID:      id,
		CallID:  callID,
		Output:  output,
		IsError: false,
	}, nil
}

func openAIRealtimeFunctionCall(item map[string]any) (*llm.FunctionCall, error) {
	id, _ := item["id"].(string)
	callID, _ := item["call_id"].(string)
	name, _ := item["name"].(string)
	arguments, _ := item["arguments"].(string)
	if id == "" || callID == "" || name == "" {
		return nil, fmt.Errorf("malformed realtime function call item")
	}
	return &llm.FunctionCall{
		ID:        id,
		CallID:    callID,
		Name:      name,
		Arguments: arguments,
	}, nil
}

func openAIRealtimeChatMessage(item map[string]any) (*llm.ChatMessage, error) {
	id, _ := item["id"].(string)
	roleRaw, _ := item["role"].(string)
	role := llm.ChatRole(roleRaw)
	switch role {
	case llm.ChatRoleSystem, llm.ChatRoleDeveloper, llm.ChatRoleUser, llm.ChatRoleAssistant:
	default:
		return nil, fmt.Errorf("unsupported role: %s", roleRaw)
	}
	contents, ok := item["content"].([]any)
	if !ok {
		return nil, fmt.Errorf("content is None")
	}
	return &llm.ChatMessage{
		ID:      id,
		Role:    role,
		Content: openAIRealtimeChatContent(role, contents),
	}, nil
}

func openAIRealtimeChatContent(role llm.ChatRole, contents []any) []llm.ChatContent {
	out := make([]llm.ChatContent, 0, len(contents))
	for _, content := range contents {
		part, ok := content.(map[string]any)
		if !ok {
			continue
		}
		partType, _ := part["type"].(string)
		switch partType {
		case "input_text", "output_text":
			text, hasText := part["text"].(string)
			if text != "" || (hasText && role == llm.ChatRoleUser && partType == "input_text") {
				out = append(out, llm.ChatContent{Text: text})
			}
		case "input_image":
			if imageURL, _ := part["image_url"].(string); imageURL != "" {
				out = append(out, llm.ChatContent{
					Image: &llm.ImageContent{Image: imageURL},
				})
			}
		case "input_audio":
			if transcript, hasTranscript := part["transcript"].(string); hasTranscript {
				out = append(out, llm.ChatContent{Text: transcript})
			}
		}
	}
	return out
}

func openAIRealtimeConfidenceFromLogprobs(v any) *float64 {
	logprobs, ok := v.([]any)
	if !ok || len(logprobs) == 0 {
		return nil
	}
	total := 0.0
	for _, item := range logprobs {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil
		}
		logprob := openAIRealtimeFloat(entry["logprob"])
		total += logprob
	}
	confidence := math.Exp(total / float64(len(logprobs)))
	return &confidence
}

func openAIRealtimeFloat(v any) float64 {
	switch value := v.(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case json.Number:
		f, _ := value.Float64()
		return f
	default:
		return 0
	}
}

func openAIRealtimeString(v any) string {
	value, _ := v.(string)
	return value
}

func openAIRealtimeInt(v any) int {
	return int(openAIRealtimeFloat(v))
}

func openAIRealtimeResponseClientEventID(response map[string]any) (string, bool) {
	metadata, ok := response["metadata"].(map[string]any)
	if !ok {
		return "", false
	}
	clientEventID, ok := metadata["client_event_id"].(string)
	if !ok || clientEventID == "" {
		return "", false
	}
	return clientEventID, true
}
