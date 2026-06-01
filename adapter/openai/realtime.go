package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/utils/images"
	"github.com/gorilla/websocket"
)

type RealtimeModel struct {
	apiKey  string
	model   string
	baseURL string
}

func NewRealtimeModel(apiKey, model string) *RealtimeModel {
	if model == "" {
		model = "gpt-4o-realtime-preview"
	}
	return &RealtimeModel{
		apiKey:  apiKey,
		model:   model,
		baseURL: "wss://api.openai.com/v1/realtime",
	}
}

func (m *RealtimeModel) Capabilities() llm.RealtimeCapabilities {
	return llm.RealtimeCapabilities{
		MessageTruncation:       true,
		TurnDetection:           true,
		UserTranscription:       true,
		AutoToolReplyGeneration: false,
		AudioOutput:             true,
		ManualFunctionCalls:     true,
		MutableChatContext:      true,
		MutableInstructions:     true,
		MutableTools:            true,
		PerResponseToolChoice:   true,
	}
}

type realtimeSession struct {
	conn             *websocket.Conn
	ctx              context.Context
	cancel           context.CancelFunc
	mu               sync.Mutex
	eventCh          chan llm.RealtimeEvent
	remote           *llm.RemoteChatContext
	inputTranscripts map[inputTranscriptKey]string
	generation       *realtimeGeneration
}

type inputTranscriptKey struct {
	itemID       string
	contentIndex int
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
	wsURL := fmt.Sprintf("%s?model=%s", m.baseURL, m.model)

	header := http.Header{}
	header.Add("Authorization", "Bearer "+m.apiKey)
	header.Add("OpenAI-Beta", "realtime=v1")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to OpenAI realtime: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &realtimeSession{
		conn:    conn,
		ctx:     ctx,
		cancel:  cancel,
		eventCh: make(chan llm.RealtimeEvent, 100),
		remote:  llm.NewRemoteChatContext(),
	}

	go s.eventLoop()

	return s, nil
}

func (s *realtimeSession) EventCh() <-chan llm.RealtimeEvent {
	return s.eventCh
}

func (s *realtimeSession) UpdateInstructions(instructions string) error {
	msg := map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"instructions": instructions,
		},
	}
	return s.sendMsg(msg)
}

func (s *realtimeSession) UpdateChatContext(chatCtx *llm.ChatContext) error {
	if s.remote == nil {
		s.remote = llm.NewRemoteChatContext()
	}
	syncedChatCtx := openAIRealtimeSyncedChatContext(chatCtx)
	msgs, err := openAIRealtimeChatContextUpdateMessages(s.remote.ToChatCtx(), syncedChatCtx)
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
		"type": "session.update",
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
	return chatCtx.Copy(llm.ChatContextCopyOptions{
		ExcludeHandoff:      true,
		ExcludeConfigUpdate: true,
	})
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
		"type":    "conversation.item.delete",
		"item_id": itemID,
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
		if part.Text != "" {
			partType := "input_text"
			if msg.Role == llm.ChatRoleAssistant {
				partType = "output_text"
			}
			content = append(content, map[string]any{
				"type": partType,
				"text": part.Text,
			})
		}
		if part.Image != nil {
			imagePart, err := openAIRealtimeImageContent(part.Image)
			if err != nil {
				return nil, err
			}
			if imagePart != nil {
				content = append(content, imagePart)
			}
		}
		if part.Audio != nil && part.Audio.Transcript != "" {
			content = append(content, map[string]any{
				"type":       "input_audio",
				"transcript": part.Audio.Transcript,
			})
		}
	}
	return content, nil
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
	return s.sendMsg(openAIRealtimeUpdateOptionsMessage(options))
}

func openAIRealtimeUpdateOptionsMessage(options llm.RealtimeSessionOptions) map[string]any {
	session := make(map[string]any)
	if toolChoice := openAIRealtimeToolChoice(options.ToolChoice); toolChoice != nil {
		session["tool_choice"] = toolChoice
	}
	return map[string]any{
		"type":    "session.update",
		"session": session,
	}
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
	msg := map[string]any{
		"type": "response.cancel",
	}
	return s.sendMsg(msg)
}

func (s *realtimeSession) PushAudio(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}

	b64Audio := base64.StdEncoding.EncodeToString(frame.Data)
	msg := map[string]any{
		"type":  "input_audio_buffer.append",
		"audio": b64Audio,
	}
	return s.sendMsg(msg)
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
		"type": "conversation.item.create",
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
	return s.sendMsg(openAIRealtimeGenerateReplyMessage(options))
}

func openAIRealtimeGenerateReplyMessage(options llm.RealtimeGenerateReplyOptions) map[string]any {
	response := make(map[string]any)
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
	return s.sendMsg(openAIRealtimeCommitAudioMessage())
}

func openAIRealtimeCommitAudioMessage() map[string]any {
	return map[string]any{
		"type": "input_audio_buffer.commit",
	}
}

func (s *realtimeSession) ClearAudio() error {
	return s.sendMsg(openAIRealtimeClearAudioMessage())
}

func openAIRealtimeClearAudioMessage() map[string]any {
	return map[string]any{
		"type": "input_audio_buffer.clear",
	}
}

func (s *realtimeSession) Close() error {
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
			realtimeEvent = s.trackRealtimeEvent(realtimeEvent)
			s.eventCh <- realtimeEvent
		}
	}
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
			s.inputTranscripts = make(map[inputTranscriptKey]string)
		}
		key := inputTranscriptKey{itemID: transcription.ItemID, contentIndex: transcription.ContentIndex}
		s.inputTranscripts[key] += transcription.Transcript
		transcription.Transcript = s.inputTranscripts[key]
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
	key := inputTranscriptKey{itemID: itemID, contentIndex: contentIndex}
	transcript, ok := s.inputTranscripts[key]
	delete(s.inputTranscripts, key)
	return transcript, ok
}

func (s *realtimeSession) clearRealtimeInputTranscripts(itemID string) {
	if itemID == "" || s.inputTranscripts == nil {
		return
	}
	for key := range s.inputTranscripts {
		if key.itemID == itemID {
			delete(s.inputTranscripts, key)
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
		return nil, fmt.Errorf("unsupported realtime item type %q", itemType)
	}
}

func openAIRealtimeFunctionCallOutput(item map[string]any) (*llm.FunctionCallOutput, error) {
	id, _ := item["id"].(string)
	callID, _ := item["call_id"].(string)
	output, _ := item["output"].(string)
	if id == "" || callID == "" {
		return nil, fmt.Errorf("malformed realtime function call output item")
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
		return nil, fmt.Errorf("unsupported realtime message role %q", roleRaw)
	}
	contents, _ := item["content"].([]any)
	return &llm.ChatMessage{
		ID:      id,
		Role:    role,
		Content: openAIRealtimeChatContent(contents),
	}, nil
}

func openAIRealtimeChatContent(contents []any) []llm.ChatContent {
	out := make([]llm.ChatContent, 0, len(contents))
	for _, content := range contents {
		part, ok := content.(map[string]any)
		if !ok {
			continue
		}
		partType, _ := part["type"].(string)
		switch partType {
		case "input_text", "output_text":
			if text, _ := part["text"].(string); text != "" {
				out = append(out, llm.ChatContent{Text: text})
			}
		case "input_image":
			if imageURL, _ := part["image_url"].(string); imageURL != "" {
				out = append(out, llm.ChatContent{
					Image: &llm.ImageContent{Image: imageURL},
				})
			}
		case "input_audio":
			if transcript, _ := part["transcript"].(string); transcript != "" {
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
