package llm

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	cavosmath "github.com/cavos-io/conversation-worker/library/math"
)

type ChatContextDictOptions struct {
	IncludeImage        bool
	IncludeAudio        bool
	IncludeTimestamp    bool
	ExcludeFunctionCall bool
	ExcludeMetrics      bool
	ExcludeConfigUpdate bool
}

type ChatMessageArgs struct {
	ID          string
	Role        ChatRole
	Content     []ChatContent
	Text        string
	Interrupted bool
	CreatedAt   time.Time
	Extra       map[string]any
	Metrics     map[string]any
}

type ChatContextCopyOptions struct {
	ExcludeFunctionCall bool
	ExcludeInstructions bool
	ExcludeEmptyMessage bool
	ExcludeHandoff      bool
	ExcludeConfigUpdate bool
	Tools               []interface{}
}

func (c *ChatContext) Copy(options ...ChatContextCopyOptions) *ChatContext {
	var opts ChatContextCopyOptions
	if len(options) > 0 {
		opts = options[0]
	}

	newCtx := NewChatContext()
	validTools := chatContextCopyToolNames(opts.Tools)
	filterByTools := opts.Tools != nil
	for _, item := range c.Items {
		if opts.ExcludeFunctionCall && isFunctionChatItem(item) {
			continue
		}
		if opts.ExcludeInstructions && isInstructionMessage(item) {
			continue
		}
		if opts.ExcludeEmptyMessage && isEmptyMessage(item) {
			continue
		}
		if opts.ExcludeHandoff && item.GetType() == "agent_handoff" {
			continue
		}
		if opts.ExcludeConfigUpdate && item.GetType() == "agent_config_update" {
			continue
		}
		if filterByTools && isFunctionChatItem(item) {
			if _, ok := validTools[functionChatItemName(item)]; !ok {
				continue
			}
		}
		newCtx.Items = append(newCtx.Items, item)
	}
	return newCtx
}

func (c *ChatContext) AddMessage(args ChatMessageArgs) *ChatMessage {
	createdAt := args.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	content := args.Content
	if len(content) == 0 && args.Text != "" {
		content = []ChatContent{{Text: args.Text}}
	}
	message := &ChatMessage{
		ID:          itemIDOrDefault(args.ID),
		Role:        args.Role,
		Content:     content,
		Interrupted: args.Interrupted,
		Extra:       args.Extra,
		Metrics:     args.Metrics,
		CreatedAt:   createdAt,
	}
	if args.CreatedAt.IsZero() {
		c.Append(message)
		return message
	}
	c.Insert(message)
	return message
}

func (c *ChatContext) Insert(items ...ChatItem) {
	for _, item := range items {
		idx := c.FindInsertionIndex(item.GetCreatedAt())
		c.Items = append(c.Items[:idx], append([]ChatItem{item}, c.Items[idx:]...)...)
	}
}

func (c *ChatContext) GetByID(itemID string) ChatItem {
	for _, item := range c.Items {
		if item.GetID() == itemID {
			return item
		}
	}
	return nil
}

func (c *ChatContext) IndexByID(itemID string) *int {
	for i, item := range c.Items {
		if item.GetID() == itemID {
			return &i
		}
	}
	return nil
}

func chatContextCopyToolNames(tools []interface{}) map[string]struct{} {
	names := make(map[string]struct{})
	for _, tool := range tools {
		switch t := tool.(type) {
		case string:
			names[t] = struct{}{}
		case Tool:
			names[t.Name()] = struct{}{}
		case Toolset:
			for _, childTool := range t.Tools() {
				names[childTool.Name()] = struct{}{}
			}
		}
	}
	return names
}

func isFunctionChatItem(item ChatItem) bool {
	return item.GetType() == "function_call" || item.GetType() == "function_call_output"
}

func isInstructionMessage(item ChatItem) bool {
	msg, ok := item.(*ChatMessage)
	return ok && (msg.Role == ChatRoleSystem || msg.Role == ChatRoleDeveloper)
}

func isEmptyMessage(item ChatItem) bool {
	msg, ok := item.(*ChatMessage)
	return ok && len(msg.Content) == 0
}

func functionChatItemName(item ChatItem) string {
	switch it := item.(type) {
	case *FunctionCall:
		return it.Name
	case *FunctionCallOutput:
		return it.Name
	default:
		return ""
	}
}

func (c *ChatContext) Messages() []*ChatMessage {
	var msgs []*ChatMessage
	for _, item := range c.Items {
		if msg, ok := item.(*ChatMessage); ok {
			msgs = append(msgs, msg)
		}
	}
	return msgs
}

func (c *ChatContext) Truncate(maxItems int) *ChatContext {
	if len(c.Items) <= maxItems {
		return c
	}

	var instructions ChatItem
	for _, item := range c.Items {
		if msg, ok := item.(*ChatMessage); ok && (msg.Role == ChatRoleSystem || msg.Role == ChatRoleDeveloper) {
			instructions = item
			break
		}
	}

	newItems := c.Items[len(c.Items)-maxItems:]

	// Don't start with function calls to avoid partial sequences.
	for len(newItems) > 0 && isFunctionChatItem(newItems[0]) {
		newItems = newItems[1:]
	}

	if instructions != nil {
		found := false
		for _, item := range newItems {
			if item.GetID() == instructions.GetID() {
				found = true
				break
			}
		}
		if !found {
			newItems = append([]ChatItem{instructions}, newItems...)
		}
	}

	c.Items = newItems
	return c
}

type ChatContextMergeOptions struct {
	ExcludeFunctionCall bool
	ExcludeInstructions bool
	ExcludeConfigUpdate bool
}

func (c *ChatContext) Merge(other *ChatContext, options ...ChatContextMergeOptions) {
	var opts ChatContextMergeOptions
	if len(options) > 0 {
		opts = options[0]
	}

	existingIDs := make(map[string]struct{})
	for _, item := range c.Items {
		existingIDs[item.GetID()] = struct{}{}
	}

	for _, item := range other.Items {
		if opts.ExcludeFunctionCall && isFunctionChatItem(item) {
			continue
		}
		if opts.ExcludeInstructions && isInstructionMessage(item) {
			continue
		}
		if opts.ExcludeConfigUpdate && item.GetType() == "agent_config_update" {
			continue
		}
		if _, ok := existingIDs[item.GetID()]; !ok {
			idx := c.FindInsertionIndex(item.GetCreatedAt())
			c.Items = append(c.Items[:idx], append([]ChatItem{item}, c.Items[idx:]...)...)
			existingIDs[item.GetID()] = struct{}{}
		}
	}
}

func (c *ChatContext) IsEquivalent(other *ChatContext) bool {
	if c == other {
		return true
	}
	if other == nil || len(c.Items) != len(other.Items) {
		return false
	}

	for i, item := range c.Items {
		otherItem := other.Items[i]
		if item.GetID() != otherItem.GetID() || item.GetType() != otherItem.GetType() {
			return false
		}

		switch a := item.(type) {
		case *ChatMessage:
			b, ok := otherItem.(*ChatMessage)
			if !ok || a.Role != b.Role || a.Interrupted != b.Interrupted || !reflect.DeepEqual(a.Content, b.Content) {
				return false
			}
		case *FunctionCall:
			b, ok := otherItem.(*FunctionCall)
			if !ok || a.Name != b.Name || a.CallID != b.CallID || a.Arguments != b.Arguments {
				return false
			}
		case *FunctionCallOutput:
			b, ok := otherItem.(*FunctionCallOutput)
			if !ok || a.Name != b.Name || a.CallID != b.CallID || a.Output != b.Output || a.IsError != b.IsError {
				return false
			}
		}
	}

	return true
}

func (c *ChatContext) ToDict(options ...ChatContextDictOptions) map[string]any {
	var opts ChatContextDictOptions
	if len(options) > 0 {
		opts = options[0]
	}

	items := make([]map[string]any, 0, len(c.Items))
	for _, item := range c.Items {
		if opts.ExcludeFunctionCall && isFunctionChatItem(item) {
			continue
		}
		if opts.ExcludeConfigUpdate && item.GetType() == "agent_config_update" {
			continue
		}
		if serialized := chatItemToDict(item, opts); serialized != nil {
			items = append(items, serialized)
		}
	}

	return map[string]any{"items": items}
}

func (c *ChatContext) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.ToDict(ChatContextDictOptions{
		IncludeImage:     true,
		IncludeAudio:     true,
		IncludeTimestamp: true,
	}))
}

func ChatContextFromDict(data map[string]any) (*ChatContext, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var ctx ChatContext
	if err := json.Unmarshal(encoded, &ctx); err != nil {
		return nil, err
	}
	return &ctx, nil
}

func (c *ChatContext) UnmarshalJSON(data []byte) error {
	var decoded struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	items := make([]ChatItem, 0, len(decoded.Items))
	for _, rawItem := range decoded.Items {
		item, err := chatItemFromJSON(rawItem)
		if err != nil {
			return err
		}
		items = append(items, item)
	}

	c.Items = items
	return nil
}

func chatItemFromJSON(data []byte) (ChatItem, error) {
	var discriminator struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return nil, err
	}

	switch discriminator.Type {
	case "message":
		return chatMessageFromJSON(data)
	case "function_call":
		var item struct {
			ID        string         `json:"id"`
			CallID    string         `json:"call_id"`
			Name      string         `json:"name"`
			Arguments string         `json:"arguments"`
			Extra     map[string]any `json:"extra"`
			GroupID   *string        `json:"group_id"`
			CreatedAt *float64       `json:"created_at"`
		}
		if err := json.Unmarshal(data, &item); err != nil {
			return nil, err
		}
		return &FunctionCall{
			ID:        itemIDOrDefault(item.ID),
			CallID:    item.CallID,
			Name:      item.Name,
			Arguments: item.Arguments,
			Extra:     item.Extra,
			GroupID:   item.GroupID,
			CreatedAt: chatItemCreatedAtOrDefault(item.CreatedAt),
		}, nil
	case "function_call_output":
		var item struct {
			ID        string   `json:"id"`
			CallID    string   `json:"call_id"`
			Name      string   `json:"name"`
			Output    string   `json:"output"`
			IsError   bool     `json:"is_error"`
			CreatedAt *float64 `json:"created_at"`
		}
		if err := json.Unmarshal(data, &item); err != nil {
			return nil, err
		}
		return &FunctionCallOutput{
			ID:        itemIDOrDefault(item.ID),
			CallID:    item.CallID,
			Name:      item.Name,
			Output:    item.Output,
			IsError:   item.IsError,
			CreatedAt: chatItemCreatedAtOrDefault(item.CreatedAt),
		}, nil
	case "agent_handoff":
		var item struct {
			ID         string   `json:"id"`
			OldAgentID *string  `json:"old_agent_id"`
			NewAgentID string   `json:"new_agent_id"`
			CreatedAt  *float64 `json:"created_at"`
		}
		if err := json.Unmarshal(data, &item); err != nil {
			return nil, err
		}
		return &AgentHandoff{
			ID:         itemIDOrDefault(item.ID),
			OldAgentID: item.OldAgentID,
			NewAgentID: item.NewAgentID,
			CreatedAt:  chatItemCreatedAtOrDefault(item.CreatedAt),
		}, nil
	case "agent_config_update":
		var item struct {
			ID           string   `json:"id"`
			Instructions *string  `json:"instructions"`
			ToolsAdded   []string `json:"tools_added"`
			ToolsRemoved []string `json:"tools_removed"`
			CreatedAt    *float64 `json:"created_at"`
		}
		if err := json.Unmarshal(data, &item); err != nil {
			return nil, err
		}
		return &AgentConfigUpdate{
			ID:           itemIDOrDefault(item.ID),
			Instructions: item.Instructions,
			ToolsAdded:   item.ToolsAdded,
			ToolsRemoved: item.ToolsRemoved,
			CreatedAt:    chatItemCreatedAtOrDefault(item.CreatedAt),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported chat item type %q", discriminator.Type)
	}
}

func chatMessageFromJSON(data []byte) (*ChatMessage, error) {
	var item struct {
		ID                   string            `json:"id"`
		Role                 ChatRole          `json:"role"`
		Content              []json.RawMessage `json:"content"`
		Interrupted          bool              `json:"interrupted"`
		TranscriptConfidence *float64          `json:"transcript_confidence"`
		Extra                map[string]any    `json:"extra"`
		Metrics              map[string]any    `json:"metrics"`
		CreatedAt            *float64          `json:"created_at"`
	}
	if err := json.Unmarshal(data, &item); err != nil {
		return nil, err
	}

	content := make([]ChatContent, 0, len(item.Content))
	for _, rawContent := range item.Content {
		parsed, err := chatContentFromJSON(rawContent)
		if err != nil {
			return nil, err
		}
		content = append(content, parsed)
	}

	return &ChatMessage{
		ID:                   itemIDOrDefault(item.ID),
		Role:                 item.Role,
		Content:              content,
		Interrupted:          item.Interrupted,
		TranscriptConfidence: item.TranscriptConfidence,
		Extra:                item.Extra,
		Metrics:              nonNilMap(item.Metrics),
		CreatedAt:            chatItemCreatedAtOrDefault(item.CreatedAt),
	}, nil
}

func chatContentFromJSON(data []byte) (ChatContent, error) {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		return ChatContent{Text: text}, nil
	}

	var discriminator struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return ChatContent{}, err
	}

	switch discriminator.Type {
	case "image_content":
		var image struct {
			ID              string `json:"id"`
			Image           any    `json:"image"`
			InferenceWidth  *int   `json:"inference_width"`
			InferenceHeight *int   `json:"inference_height"`
			InferenceDetail string `json:"inference_detail"`
			MimeType        string `json:"mime_type"`
		}
		if err := json.Unmarshal(data, &image); err != nil {
			return ChatContent{}, err
		}
		return ChatContent{Image: &ImageContent{
			ID:              imageIDOrDefault(image.ID),
			Image:           image.Image,
			InferenceWidth:  image.InferenceWidth,
			InferenceHeight: image.InferenceHeight,
			InferenceDetail: imageInferenceDetailOrDefault(image.InferenceDetail),
			MimeType:        image.MimeType,
		}}, nil
	case "audio_content":
		var audio struct {
			Frames     []any  `json:"frame"`
			Transcript string `json:"transcript"`
		}
		if err := json.Unmarshal(data, &audio); err != nil {
			return ChatContent{}, err
		}
		return ChatContent{Audio: &AudioContent{
			Frames:     audio.Frames,
			Transcript: audio.Transcript,
		}}, nil
	default:
		return ChatContent{}, fmt.Errorf("unsupported chat content type %q", discriminator.Type)
	}
}

func chatItemToDict(item ChatItem, opts ChatContextDictOptions) map[string]any {
	switch it := item.(type) {
	case *ChatMessage:
		it.ID = itemIDOrDefault(it.ID)
		data := map[string]any{
			"id":          it.ID,
			"type":        "message",
			"role":        string(it.Role),
			"content":     chatContentToDict(it.Content, opts),
			"interrupted": it.Interrupted,
			"extra":       nonNilMap(it.Extra),
		}
		if !opts.ExcludeMetrics {
			data["metrics"] = nonNilMap(it.Metrics)
		}
		if it.TranscriptConfidence != nil {
			data["transcript_confidence"] = *it.TranscriptConfidence
		}
		addCreatedAt(data, it.CreatedAt, opts)
		return data
	case *FunctionCall:
		it.ID = itemIDOrDefault(it.ID)
		data := map[string]any{
			"id":        it.ID,
			"type":      "function_call",
			"call_id":   it.CallID,
			"arguments": it.Arguments,
			"name":      it.Name,
			"extra":     nonNilMap(it.Extra),
		}
		if it.GroupID != nil {
			data["group_id"] = *it.GroupID
		}
		addCreatedAt(data, it.CreatedAt, opts)
		return data
	case *FunctionCallOutput:
		it.ID = itemIDOrDefault(it.ID)
		data := map[string]any{
			"id":       it.ID,
			"type":     "function_call_output",
			"name":     it.Name,
			"call_id":  it.CallID,
			"output":   it.Output,
			"is_error": it.IsError,
		}
		addCreatedAt(data, it.CreatedAt, opts)
		return data
	case *AgentHandoff:
		it.ID = itemIDOrDefault(it.ID)
		data := map[string]any{
			"id":           it.ID,
			"type":         "agent_handoff",
			"new_agent_id": it.NewAgentID,
		}
		if it.OldAgentID != nil {
			data["old_agent_id"] = *it.OldAgentID
		}
		addCreatedAt(data, it.CreatedAt, opts)
		return data
	case *AgentConfigUpdate:
		it.ID = itemIDOrDefault(it.ID)
		data := map[string]any{
			"id":   it.ID,
			"type": "agent_config_update",
		}
		if it.Instructions != nil {
			data["instructions"] = *it.Instructions
		}
		if it.ToolsAdded != nil {
			data["tools_added"] = it.ToolsAdded
		}
		if it.ToolsRemoved != nil {
			data["tools_removed"] = it.ToolsRemoved
		}
		addCreatedAt(data, it.CreatedAt, opts)
		return data
	default:
		return nil
	}
}

func chatContentToDict(content []ChatContent, opts ChatContextDictOptions) []any {
	serialized := make([]any, 0, len(content))
	for _, item := range content {
		if item.Text != "" {
			serialized = append(serialized, item.Text)
		}
		if opts.IncludeImage && item.Image != nil {
			serialized = append(serialized, imageContentToDict(item.Image))
		}
		if opts.IncludeAudio && item.Audio != nil {
			serialized = append(serialized, audioContentToDict(item.Audio))
		}
	}
	return serialized
}

func imageContentToDict(image *ImageContent) map[string]any {
	image.ID = imageIDOrDefault(image.ID)
	data := map[string]any{
		"id":               image.ID,
		"type":             "image_content",
		"image":            image.Image,
		"inference_detail": imageInferenceDetailOrDefault(image.InferenceDetail),
	}
	if image.InferenceWidth != nil {
		data["inference_width"] = *image.InferenceWidth
	}
	if image.InferenceHeight != nil {
		data["inference_height"] = *image.InferenceHeight
	}
	if image.MimeType != "" {
		data["mime_type"] = image.MimeType
	}
	return data
}

func itemIDOrDefault(id string) string {
	if id == "" {
		return cavosmath.ShortUUID("item_")
	}
	return id
}

func imageIDOrDefault(id string) string {
	if id == "" {
		return cavosmath.ShortUUID("img_")
	}
	return id
}

func imageInferenceDetailOrDefault(detail string) string {
	if detail == "" {
		return "auto"
	}
	return detail
}

func audioContentToDict(audio *AudioContent) map[string]any {
	data := map[string]any{
		"type":  "audio_content",
		"frame": audio.Frames,
	}
	if audio.Transcript != "" {
		data["transcript"] = audio.Transcript
	}
	return data
}

func addCreatedAt(data map[string]any, createdAt time.Time, opts ChatContextDictOptions) {
	if opts.IncludeTimestamp {
		data["created_at"] = float64(createdAt.UnixNano()) / float64(time.Second)
	}
}

func unixSecondsToTime(seconds *float64) time.Time {
	if seconds == nil {
		return time.Time{}
	}
	nanos := int64(*seconds * float64(time.Second))
	return time.Unix(0, nanos)
}

func chatItemCreatedAtOrDefault(seconds *float64) time.Time {
	if seconds == nil {
		return time.Now()
	}
	return unixSecondsToTime(seconds)
}

func nonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func (c *ChatContext) FindInsertionIndex(createdAt time.Time) int {
	for i := len(c.Items) - 1; i >= 0; i-- {
		if !c.Items[i].GetCreatedAt().After(createdAt) {
			return i + 1
		}
	}
	return 0
}

func (c *ChatContext) ToProviderFormat(format string) ([]map[string]any, any) {
	if format == "openai" {
		messages := make([]map[string]any, 0)
		for _, group := range groupOpenAIToolCalls(c.Items) {
			if group.message == nil && len(group.toolCalls) == 0 && len(group.toolOutputs) == 0 {
				continue
			}

			var msg map[string]any
			if group.message != nil {
				msg = openAIChatMessage(group.message)
			} else {
				msg = map[string]any{"role": "assistant"}
			}

			if len(group.toolCalls) > 0 {
				toolCalls := make([]map[string]any, 0, len(group.toolCalls))
				for _, toolCall := range group.toolCalls {
					toolCalls = append(toolCalls, openAIToolCall(toolCall))
				}
				msg["tool_calls"] = toolCalls
			}
			messages = append(messages, msg)

			for _, toolOutput := range group.toolOutputs {
				messages = append(messages, openAIToolOutput(toolOutput))
			}
		}
		return messages, nil
	}
	if format == "openai.responses" {
		items := make([]map[string]any, 0)
		for _, group := range groupOpenAIToolCalls(c.Items) {
			if group.message == nil && len(group.toolCalls) == 0 && len(group.toolOutputs) == 0 {
				continue
			}
			if group.message != nil {
				items = append(items, openAIResponsesMessage(group.message))
			}
			for _, toolCall := range group.toolCalls {
				items = append(items, openAIResponsesToolCall(toolCall))
			}
			for _, toolOutput := range group.toolOutputs {
				items = append(items, openAIResponsesToolOutput(toolOutput))
			}
		}
		return items, nil
	}
	if format == "google" {
		return c.toGoogleProviderFormat()
	}
	if format == "anthropic" {
		return c.toAnthropicProviderFormat()
	}
	if format == "aws" {
		return c.toAWSProviderFormat()
	}
	return nil, nil
}

func (c *ChatContext) toGoogleProviderFormat() ([]map[string]any, any) {
	turns := make([]map[string]any, 0)
	systemMessages := make([]string, 0)
	currentRole := ""
	parts := make([]map[string]any, 0)

	flush := func() {
		if currentRole == "" || len(parts) == 0 {
			return
		}
		role := currentRole
		if role == "tool" {
			role = "user"
		}
		turns = append(turns, map[string]any{
			"role":  role,
			"parts": parts,
		})
		parts = make([]map[string]any, 0)
	}

	for _, group := range groupOpenAIToolCalls(c.Items) {
		for _, item := range group.flatten() {
			if msg, ok := item.(*ChatMessage); ok && msg.Role == ChatRoleSystem && msg.TextContent() != "" {
				systemMessages = append(systemMessages, msg.TextContent())
				continue
			}

			role := googleItemRole(item)
			if role == "" {
				continue
			}
			if role != currentRole {
				flush()
				currentRole = role
			}
			parts = append(parts, googleItemParts(item)...)
		}
	}
	flush()

	if currentRole != "user" && currentRole != "tool" {
		turns = append(turns, map[string]any{
			"role":  "user",
			"parts": []map[string]any{{"text": "."}},
		})
	}

	return turns, map[string]any{"system_messages": systemMessages}
}

func (c *ChatContext) toAnthropicProviderFormat() ([]map[string]any, any) {
	messages := make([]map[string]any, 0)
	systemMessages := make([]string, 0)
	currentRole := ""
	content := make([]map[string]any, 0)

	flush := func() {
		if currentRole == "" || len(content) == 0 {
			return
		}
		messages = append(messages, map[string]any{
			"role":    currentRole,
			"content": content,
		})
		content = make([]map[string]any, 0)
	}

	for _, group := range groupOpenAIToolCalls(c.Items) {
		for _, item := range group.flatten() {
			if msg, ok := item.(*ChatMessage); ok && msg.Role == ChatRoleSystem && msg.TextContent() != "" {
				systemMessages = append(systemMessages, msg.TextContent())
				continue
			}

			role := anthropicItemRole(item)
			if role == "" {
				continue
			}
			if role != currentRole {
				flush()
				currentRole = role
			}
			content = append(content, anthropicItemContent(item)...)
		}
	}
	flush()

	if len(messages) == 0 || messages[0]["role"] != "user" {
		messages = append([]map[string]any{{
			"role":    "user",
			"content": []map[string]any{{"text": "(empty)", "type": "text"}},
		}}, messages...)
	}

	return messages, map[string]any{"system_messages": systemMessages}
}

func (c *ChatContext) toAWSProviderFormat() ([]map[string]any, any) {
	messages := make([]map[string]any, 0)
	systemMessages := make([]string, 0)
	currentRole := ""
	content := make([]map[string]any, 0)

	flush := func() {
		if currentRole == "" || len(content) == 0 {
			return
		}
		messages = append(messages, map[string]any{
			"role":    currentRole,
			"content": content,
		})
		content = make([]map[string]any, 0)
	}

	for _, group := range groupOpenAIToolCalls(c.Items) {
		for _, item := range group.flatten() {
			if msg, ok := item.(*ChatMessage); ok && msg.Role == ChatRoleSystem && msg.TextContent() != "" {
				systemMessages = append(systemMessages, msg.TextContent())
				continue
			}

			role := awsItemRole(item)
			if role == "" {
				continue
			}
			if role != currentRole {
				flush()
				currentRole = role
			}
			content = append(content, awsItemContent(item)...)
		}
	}
	flush()

	if len(messages) == 0 || messages[0]["role"] != "user" {
		messages = append([]map[string]any{{
			"role":    "user",
			"content": []map[string]any{{"text": "(empty)"}},
		}}, messages...)
	}

	return messages, map[string]any{"system_messages": systemMessages}
}

type openAIToolCallGroup struct {
	message     *ChatMessage
	toolCalls   []*FunctionCall
	toolOutputs []*FunctionCallOutput
}

func groupOpenAIToolCalls(items []ChatItem) []*openAIToolCallGroup {
	groups := make([]*openAIToolCallGroup, 0)
	groupsByID := make(map[string]*openAIToolCallGroup)
	toolOutputs := make([]*FunctionCallOutput, 0)

	addToGroup := func(groupID string, item ChatItem) {
		group := groupsByID[groupID]
		if group == nil {
			group = &openAIToolCallGroup{}
			groupsByID[groupID] = group
			groups = append(groups, group)
		}
		group.add(item)
	}

	for _, item := range items {
		switch it := item.(type) {
		case *ChatMessage:
			if it.Role == ChatRoleAssistant {
				addToGroup(openAIToolGroupID(it.ID, nil), it)
			} else {
				addToGroup(it.ID, it)
			}
		case *FunctionCall:
			addToGroup(openAIToolGroupID(it.ID, it.GroupID), it)
		case *FunctionCallOutput:
			toolOutputs = append(toolOutputs, it)
		}
	}

	groupsByCallID := make(map[string]*openAIToolCallGroup)
	for _, group := range groups {
		for _, toolCall := range group.toolCalls {
			groupsByCallID[toolCall.CallID] = group
		}
	}
	for _, toolOutput := range toolOutputs {
		if group := groupsByCallID[toolOutput.CallID]; group != nil {
			group.add(toolOutput)
		}
	}
	for _, group := range groups {
		group.removeInvalidToolItems()
	}
	return groups
}

func (g *openAIToolCallGroup) add(item ChatItem) {
	switch it := item.(type) {
	case *ChatMessage:
		g.message = it
	case *FunctionCall:
		g.toolCalls = append(g.toolCalls, it)
	case *FunctionCallOutput:
		g.toolOutputs = append(g.toolOutputs, it)
	}
}

func (g *openAIToolCallGroup) flatten() []ChatItem {
	items := make([]ChatItem, 0, 1+len(g.toolCalls)+len(g.toolOutputs))
	if g.message != nil {
		items = append(items, g.message)
	}
	for _, toolCall := range g.toolCalls {
		items = append(items, toolCall)
	}
	for _, toolOutput := range g.toolOutputs {
		items = append(items, toolOutput)
	}
	return items
}

func (g *openAIToolCallGroup) removeInvalidToolItems() {
	if len(g.toolCalls) == len(g.toolOutputs) {
		return
	}

	outputsByCallID := make(map[string]*FunctionCallOutput)
	for _, toolOutput := range g.toolOutputs {
		outputsByCallID[toolOutput.CallID] = toolOutput
	}

	validCalls := make([]*FunctionCall, 0, len(g.toolCalls))
	validOutputs := make([]*FunctionCallOutput, 0, len(g.toolOutputs))
	for _, toolCall := range g.toolCalls {
		if toolOutput := outputsByCallID[toolCall.CallID]; toolOutput != nil {
			validCalls = append(validCalls, toolCall)
			validOutputs = append(validOutputs, toolOutput)
		}
	}

	g.toolCalls = validCalls
	g.toolOutputs = validOutputs
}

func openAIToolGroupID(itemID string, groupID *string) string {
	if groupID != nil && *groupID != "" {
		return *groupID
	}
	for i, r := range itemID {
		if r == '/' {
			return itemID[:i]
		}
	}
	return itemID
}

func openAIChatMessage(msg *ChatMessage) map[string]any {
	content := openAIChatContent(msg.Content)
	result := map[string]any{
		"role":    string(msg.Role),
		"content": content,
	}
	if extra := openAIExtraContent(msg.Extra); len(extra) > 0 {
		result["extra_content"] = extra
	}
	return result
}

func openAIChatContent(content []ChatContent) any {
	parts := make([]map[string]any, 0)
	textContent := ""
	for _, item := range content {
		if item.Text != "" {
			if textContent != "" {
				textContent += "\n"
			}
			textContent += item.Text
		}
		if item.Image != nil {
			if part := openAIImageContent(item.Image); part != nil {
				parts = append(parts, part)
			}
		}
	}
	if len(parts) == 0 {
		return textContent
	}
	if textContent != "" {
		parts = append(parts, map[string]any{
			"type": "text",
			"text": textContent,
		})
	}
	return parts
}

func openAIImageContent(image *ImageContent) map[string]any {
	url, ok := image.Image.(string)
	if !ok || url == "" {
		return nil
	}
	return map[string]any{
		"type": "image_url",
		"image_url": map[string]any{
			"url":    url,
			"detail": imageInferenceDetailOrDefault(image.InferenceDetail),
		},
	}
}

func openAIResponsesMessage(msg *ChatMessage) map[string]any {
	return map[string]any{
		"role":    string(msg.Role),
		"content": openAIResponsesContent(msg.Content),
	}
}

func openAIResponsesContent(content []ChatContent) any {
	parts := make([]map[string]any, 0)
	textContent := ""
	for _, item := range content {
		if item.Text != "" {
			if textContent != "" {
				textContent += "\n"
			}
			textContent += item.Text
		}
		if item.Image != nil {
			if part := openAIResponsesImageContent(item.Image); part != nil {
				parts = append(parts, part)
			}
		}
	}
	if len(parts) == 0 {
		return textContent
	}
	if textContent != "" {
		parts = append(parts, map[string]any{
			"type": "input_text",
			"text": textContent,
		})
	}
	return parts
}

func openAIResponsesImageContent(image *ImageContent) map[string]any {
	url, ok := image.Image.(string)
	if !ok || url == "" {
		return nil
	}
	return map[string]any{
		"type":      "input_image",
		"image_url": url,
		"detail":    imageInferenceDetailOrDefault(image.InferenceDetail),
	}
}

func openAIResponsesToolCall(toolCall *FunctionCall) map[string]any {
	return map[string]any{
		"call_id":   toolCall.CallID,
		"type":      "function_call",
		"name":      toolCall.Name,
		"arguments": toolCall.Arguments,
	}
}

func openAIResponsesToolOutput(toolOutput *FunctionCallOutput) map[string]any {
	return map[string]any{
		"type":    "function_call_output",
		"call_id": toolOutput.CallID,
		"output":  toolOutput.Output,
	}
}

func openAIToolCall(toolCall *FunctionCall) map[string]any {
	result := map[string]any{
		"id":   toolCall.CallID,
		"type": "function",
		"function": map[string]any{
			"name":      toolCall.Name,
			"arguments": toolCall.Arguments,
		},
	}
	if extra := openAIExtraContent(toolCall.Extra); len(extra) > 0 {
		result["extra_content"] = extra
	}
	return result
}

func openAIExtraContent(extra map[string]any) map[string]any {
	if len(extra) == 0 {
		return nil
	}
	filtered := make(map[string]any)
	for _, key := range []string{"google", "livekit", "xai"} {
		if value, ok := extra[key]; ok && value != nil {
			filtered[key] = value
		}
	}
	return filtered
}

func googleItemRole(item ChatItem) string {
	switch it := item.(type) {
	case *ChatMessage:
		if it.Role == ChatRoleAssistant {
			return "model"
		}
		return "user"
	case *FunctionCall:
		return "model"
	case *FunctionCallOutput:
		return "tool"
	default:
		return ""
	}
}

func googleItemParts(item ChatItem) []map[string]any {
	switch it := item.(type) {
	case *ChatMessage:
		parts := make([]map[string]any, 0, len(it.Content))
		for _, content := range it.Content {
			if content.Text != "" {
				parts = append(parts, map[string]any{"text": content.Text})
			}
			if content.Image != nil {
				if part := googleImagePart(content.Image); part != nil {
					parts = append(parts, part)
				}
			}
		}
		return parts
	case *FunctionCall:
		args := map[string]any{}
		if it.Arguments != "" {
			_ = json.Unmarshal([]byte(it.Arguments), &args)
		}
		return []map[string]any{{
			"function_call": map[string]any{
				"id":   it.CallID,
				"name": it.Name,
				"args": args,
			},
		}}
	case *FunctionCallOutput:
		responseKey := "output"
		if it.IsError {
			responseKey = "error"
		}
		return []map[string]any{{
			"function_response": map[string]any{
				"id":   it.CallID,
				"name": it.Name,
				"response": map[string]any{
					responseKey: it.Output,
				},
			},
		}}
	default:
		return nil
	}
}

func googleImagePart(image *ImageContent) map[string]any {
	url, ok := image.Image.(string)
	if !ok || url == "" {
		return nil
	}
	mimeType := image.MimeType
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	return map[string]any{
		"file_data": map[string]any{
			"file_uri":  url,
			"mime_type": mimeType,
		},
	}
}

func anthropicItemRole(item ChatItem) string {
	switch it := item.(type) {
	case *ChatMessage:
		if it.Role == ChatRoleAssistant {
			return "assistant"
		}
		return "user"
	case *FunctionCall:
		return "assistant"
	case *FunctionCallOutput:
		return "user"
	default:
		return ""
	}
}

func anthropicItemContent(item ChatItem) []map[string]any {
	switch it := item.(type) {
	case *ChatMessage:
		content := make([]map[string]any, 0, len(it.Content))
		for _, item := range it.Content {
			if item.Text != "" {
				content = append(content, map[string]any{
					"text": item.Text,
					"type": "text",
				})
			}
			if item.Image != nil {
				if image := anthropicImageContent(item.Image); image != nil {
					content = append(content, image)
				}
			}
		}
		return content
	case *FunctionCall:
		input := map[string]any{}
		if it.Arguments != "" {
			_ = json.Unmarshal([]byte(it.Arguments), &input)
		}
		return []map[string]any{{
			"id":    it.CallID,
			"type":  "tool_use",
			"name":  it.Name,
			"input": input,
		}}
	case *FunctionCallOutput:
		return []map[string]any{{
			"tool_use_id": it.CallID,
			"type":        "tool_result",
			"content":     anthropicToolResultContent(it.Output),
			"is_error":    it.IsError,
		}}
	default:
		return nil
	}
}

func anthropicImageContent(image *ImageContent) map[string]any {
	url, ok := image.Image.(string)
	if !ok || url == "" {
		return nil
	}
	return map[string]any{
		"type": "image",
		"source": map[string]any{
			"type": "url",
			"url":  url,
		},
	}
}

func anthropicToolResultContent(output string) any {
	var parsed []any
	if err := json.Unmarshal([]byte(output), &parsed); err == nil {
		return parsed
	}
	return output
}

func awsItemRole(item ChatItem) string {
	switch it := item.(type) {
	case *ChatMessage:
		if it.Role == ChatRoleAssistant {
			return "assistant"
		}
		return "user"
	case *FunctionCall:
		return "assistant"
	case *FunctionCallOutput:
		return "user"
	default:
		return ""
	}
}

func awsItemContent(item ChatItem) []map[string]any {
	switch it := item.(type) {
	case *ChatMessage:
		content := make([]map[string]any, 0, len(it.Content))
		for _, item := range it.Content {
			if item.Text != "" {
				content = append(content, map[string]any{"text": item.Text})
			}
		}
		return content
	case *FunctionCall:
		input := map[string]any{}
		if it.Arguments != "" {
			_ = json.Unmarshal([]byte(it.Arguments), &input)
		}
		return []map[string]any{{
			"toolUse": map[string]any{
				"toolUseId": it.CallID,
				"name":      it.Name,
				"input":     input,
			},
		}}
	case *FunctionCallOutput:
		return []map[string]any{{
			"toolResult": map[string]any{
				"toolUseId": it.CallID,
				"content": []map[string]any{
					{"text": it.Output},
				},
				"status": "success",
			},
		}}
	default:
		return nil
	}
}

func openAIToolOutput(toolOutput *FunctionCallOutput) map[string]any {
	return map[string]any{
		"role":         "tool",
		"tool_call_id": toolOutput.CallID,
		"content":      toolOutput.Output,
	}
}
