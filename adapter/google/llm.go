package google

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"os"

	"github.com/cavos-io/rtp-agent/core/llm"
	"google.golang.org/genai"
)

type GoogleLLM struct {
	client *genai.Client
	model  string
}

func NewGoogleLLM(apiKey string, model string) (*GoogleLLM, error) {
	if model == "" {
		model = "gemini-2.5-flash"
	}
	resolvedAPIKey := resolveGoogleAPIKey(apiKey)
	if resolvedAPIKey == "" {
		return nil, errors.New("google API key is required either via api_key or GOOGLE_API_KEY environment variable")
	}
	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  resolvedAPIKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, err
	}
	return &GoogleLLM{
		client: client,
		model:  model,
	}, nil
}

func resolveGoogleAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv("GOOGLE_API_KEY")
}

func (l *GoogleLLM) Model() string { return l.model }
func (l *GoogleLLM) Provider() string {
	return "google"
}

func (l *GoogleLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	options := &llm.ChatOptions{}
	for _, opt := range opts {
		opt(options)
	}

	contents, systemInstructions := buildGoogleContents(chatCtx)

	config := &genai.GenerateContentConfig{}
	if systemInstructions != "" {
		config.SystemInstruction = genai.NewContentFromText(systemInstructions, genai.RoleUser)
	}

	if len(options.Tools) > 0 {
		declarations := make([]*genai.FunctionDeclaration, 0)
		for _, t := range options.Tools {
			declarations = append(declarations, buildGoogleFunctionDeclaration(t))
		}

		config.Tools = []*genai.Tool{
			{FunctionDeclarations: declarations},
		}
	}
	if toolConfig := buildGoogleToolConfig(options.Tools, options.ToolChoice); toolConfig != nil {
		config.ToolConfig = toolConfig
	}

	stream := l.client.Models.GenerateContentStream(ctx, l.model, contents, config)

	next, stop := iter.Pull2(stream)

	return &googleLLMStream{
		next: next,
		stop: stop,
	}, nil
}

func buildGoogleFunctionDeclaration(t llm.Tool) *genai.FunctionDeclaration {
	schemaMap := t.Parameters()
	var properties map[string]*genai.Schema

	if props, ok := schemaMap["properties"].(map[string]any); ok {
		properties = make(map[string]*genai.Schema)
		for k, v := range props {
			if typeMap, ok := v.(map[string]any); ok {
				typeStr, _ := typeMap["type"].(string)
				descStr, _ := typeMap["description"].(string)
				properties[k] = &genai.Schema{
					Type:        genai.Type(typeStr),
					Description: descStr,
				}
			}
		}
	}

	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type:       genai.TypeObject,
			Properties: properties,
			Required:   googleRequiredFields(schemaMap["required"]),
		},
	}
}

func googleRequiredFields(value any) []string {
	switch reqs := value.(type) {
	case []string:
		return append([]string(nil), reqs...)
	case []any:
		required := make([]string, 0, len(reqs))
		for _, r := range reqs {
			if reqStr, ok := r.(string); ok {
				required = append(required, reqStr)
			}
		}
		return required
	default:
		return nil
	}
}

func buildGoogleToolConfig(tools []llm.Tool, choice llm.ToolChoice) *genai.ToolConfig {
	switch tc := choice.(type) {
	case string:
		switch tc {
		case "auto":
			return &genai.ToolConfig{
				FunctionCallingConfig: &genai.FunctionCallingConfig{
					Mode: genai.FunctionCallingConfigModeAuto,
				},
			}
		case "required":
			return &genai.ToolConfig{
				FunctionCallingConfig: &genai.FunctionCallingConfig{
					Mode:                 genai.FunctionCallingConfigModeAny,
					AllowedFunctionNames: googleToolNames(tools),
				},
			}
		case "none":
			return &genai.ToolConfig{
				FunctionCallingConfig: &genai.FunctionCallingConfig{
					Mode: genai.FunctionCallingConfigModeNone,
				},
			}
		}
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
		return &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode:                 genai.FunctionCallingConfigModeAny,
				AllowedFunctionNames: []string{name},
			},
		}
	}
	return nil
}

func googleToolNames(tools []llm.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name())
	}
	if len(names) == 0 {
		return nil
	}
	return names
}

type googleLLMStream struct {
	next func() (*genai.GenerateContentResponse, error, bool)
	stop func()
}

func buildGoogleContents(chatCtx *llm.ChatContext) ([]*genai.Content, string) {
	contents := make([]*genai.Content, 0, len(chatCtx.Items))
	var systemInstructions string
	var currentRole genai.Role
	parts := make([]*genai.Part, 0)

	flush := func() {
		if currentRole == "" || len(parts) == 0 {
			return
		}
		contents = append(contents, genai.NewContentFromParts(parts, currentRole))
		parts = nil
	}

	appendParts := func(role genai.Role, newParts ...*genai.Part) {
		if currentRole == "" || currentRole != role {
			flush()
			currentRole = role
			parts = make([]*genai.Part, 0, len(newParts))
		}
		parts = append(parts, newParts...)
	}

	for _, group := range groupGoogleChatItems(chatCtx.Items) {
		for _, item := range group.flatten() {
			switch msg := item.(type) {
			case *llm.ChatMessage:
				if msg.Role == llm.ChatRoleSystem || msg.Role == llm.ChatRoleDeveloper {
					if text := msg.TextContent(); text != "" {
						systemInstructions += text + "\n"
					}
					continue
				}
				role := genai.Role(genai.RoleUser)
				if msg.Role == llm.ChatRoleAssistant {
					role = genai.Role(genai.RoleModel)
				}
				messageParts := googleMessageParts(msg)
				if len(messageParts) > 0 {
					appendParts(role, messageParts...)
				}
			case *llm.FunctionCall:
				appendParts(genai.Role(genai.RoleModel), googleFunctionCallPart(msg))
			case *llm.FunctionCallOutput:
				appendParts(genai.Role(genai.RoleUser), googleFunctionResponsePart(msg))
			}
		}
	}
	flush()

	if currentRole != genai.Role(genai.RoleUser) {
		contents = append(contents, genai.NewContentFromParts([]*genai.Part{genai.NewPartFromText(".")}, genai.Role(genai.RoleUser)))
	}

	return contents, systemInstructions
}

func googleMessageParts(msg *llm.ChatMessage) []*genai.Part {
	parts := make([]*genai.Part, 0, len(msg.Content))
	for _, content := range msg.Content {
		if content.Text != "" {
			parts = append(parts, genai.NewPartFromText(content.Text))
		}
		if content.Image != nil {
			if part := googleImagePart(content.Image); part != nil {
				parts = append(parts, part)
			}
		}
	}
	return parts
}

func googleImagePart(image *llm.ImageContent) *genai.Part {
	img, err := llm.SerializeImage(image)
	if err != nil {
		return nil
	}
	if img.ExternalURL == "" {
		return genai.NewPartFromBytes(img.DataBytes, img.MIMEType)
	}
	mimeType := img.MIMEType
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	return genai.NewPartFromURI(img.ExternalURL, mimeType)
}

func googleFunctionCallPart(fc *llm.FunctionCall) *genai.Part {
	args := make(map[string]any)
	_ = json.Unmarshal([]byte(fc.Arguments), &args)
	part := genai.NewPartFromFunctionCall(fc.Name, args)
	part.FunctionCall.ID = fc.CallID
	return part
}

func googleFunctionResponsePart(fco *llm.FunctionCallOutput) *genai.Part {
	response := map[string]any{"output": fco.Output}
	if fco.IsError {
		response = map[string]any{"error": fco.Output}
	}
	part := genai.NewPartFromFunctionResponse(fco.Name, response)
	part.FunctionResponse.ID = fco.CallID
	return part
}

type googleChatItemGroup struct {
	message     *llm.ChatMessage
	toolCalls   []*llm.FunctionCall
	toolOutputs []*llm.FunctionCallOutput
}

func groupGoogleChatItems(items []llm.ChatItem) []*googleChatItemGroup {
	groups := make([]*googleChatItemGroup, 0)
	groupsByID := make(map[string]*googleChatItemGroup)
	toolOutputs := make([]*llm.FunctionCallOutput, 0)

	addToGroup := func(groupID string, item llm.ChatItem) {
		group := groupsByID[groupID]
		if group == nil {
			group = &googleChatItemGroup{}
			groupsByID[groupID] = group
			groups = append(groups, group)
		}
		group.add(item)
	}

	for _, item := range items {
		switch it := item.(type) {
		case *llm.ChatMessage:
			if it.Role == llm.ChatRoleAssistant {
				addToGroup(googleGroupID(it.ID, nil), it)
			} else {
				addToGroup(it.ID, it)
			}
		case *llm.FunctionCall:
			addToGroup(googleGroupID(it.ID, it.GroupID), it)
		case *llm.FunctionCallOutput:
			toolOutputs = append(toolOutputs, it)
		}
	}

	groupsByCallID := make(map[string]*googleChatItemGroup)
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

func (g *googleChatItemGroup) add(item llm.ChatItem) {
	switch it := item.(type) {
	case *llm.ChatMessage:
		g.message = it
	case *llm.FunctionCall:
		g.toolCalls = append(g.toolCalls, it)
	case *llm.FunctionCallOutput:
		g.toolOutputs = append(g.toolOutputs, it)
	}
}

func (g *googleChatItemGroup) flatten() []llm.ChatItem {
	items := make([]llm.ChatItem, 0, 1+len(g.toolCalls)+len(g.toolOutputs))
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

func (g *googleChatItemGroup) removeInvalidToolItems() {
	if len(g.toolCalls) == len(g.toolOutputs) {
		return
	}

	outputsByCallID := make(map[string]*llm.FunctionCallOutput)
	for _, toolOutput := range g.toolOutputs {
		outputsByCallID[toolOutput.CallID] = toolOutput
	}

	validCalls := make([]*llm.FunctionCall, 0, len(g.toolCalls))
	validOutputs := make([]*llm.FunctionCallOutput, 0, len(g.toolOutputs))
	for _, toolCall := range g.toolCalls {
		if toolOutput := outputsByCallID[toolCall.CallID]; toolOutput != nil {
			validCalls = append(validCalls, toolCall)
			validOutputs = append(validOutputs, toolOutput)
		}
	}

	g.toolCalls = validCalls
	g.toolOutputs = validOutputs
}

func googleGroupID(itemID string, groupID *string) string {
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

func (s *googleLLMStream) Next() (*llm.ChatChunk, error) {
	resp, err, ok := s.next()
	if !ok {
		return nil, io.EOF
	}
	if err != nil {
		if errors.Is(err, genai.ErrPageDone) || errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, err
	}

	chunk := &llm.ChatChunk{
		Delta: &llm.ChoiceDelta{
			Role: llm.ChatRoleAssistant,
		},
	}

	if len(resp.Candidates) > 0 {
		cand := resp.Candidates[0]
		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				if part.Text != "" {
					chunk.Delta.Content += part.Text
				} else if part.FunctionCall != nil {
					args, _ := json.Marshal(part.FunctionCall.Args)
					chunk.Delta.ToolCalls = append(chunk.Delta.ToolCalls, llm.FunctionToolCall{
						Name:      part.FunctionCall.Name,
						Arguments: string(args),
						Type:      "function",
						CallID:    "call_" + part.FunctionCall.Name, // Gemini doesn't always provide CallID natively like OpenAI
					})
				}
			}
		}
	}

	if resp.UsageMetadata != nil {
		chunk.Usage = &llm.CompletionUsage{
			PromptTokens:     int(resp.UsageMetadata.PromptTokenCount),
			CompletionTokens: int(resp.UsageMetadata.CandidatesTokenCount),
			TotalTokens:      int(resp.UsageMetadata.TotalTokenCount),
		}
	}

	return chunk, nil
}

func (s *googleLLMStream) Close() error {
	if s.stop != nil {
		s.stop()
	}
	return nil
}
