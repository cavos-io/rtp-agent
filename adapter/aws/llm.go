package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/cavos-io/rtp-agent/core/llm"
)

const (
	defaultAWSRegion   = "us-east-1"
	defaultAWSLLMModel = "amazon.nova-2-lite-v1:0"
)

type AWSLLM struct {
	client             awsLLMClient
	model              string
	credentials        AWSCredentials
	credentialsSet     bool
	toolChoice         llm.ToolChoice
	maxOutputTokens    int32
	maxOutputTokensSet bool
	temperature        float32
	temperatureSet     bool
	topP               float32
	topPSet            bool
	additionalFields   any
	cacheSystem        bool
	cacheTools         bool
}

type AWSLLMOption func(*AWSLLM)

type awsLLMClient interface {
	ConverseStream(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error)
}

func WithAWSLLMToolChoice(toolChoice llm.ToolChoice) AWSLLMOption {
	return func(l *AWSLLM) {
		l.toolChoice = toolChoice
	}
}

func WithAWSLLMCredentials(creds AWSCredentials) AWSLLMOption {
	return func(l *AWSLLM) {
		if creds.valid() {
			l.credentials = creds
			l.credentialsSet = true
		}
	}
}

func WithAWSLLMMaxOutputTokens(maxOutputTokens int32) AWSLLMOption {
	return func(l *AWSLLM) {
		l.maxOutputTokens = maxOutputTokens
		l.maxOutputTokensSet = true
	}
}

func WithAWSLLMTemperature(temperature float32) AWSLLMOption {
	return func(l *AWSLLM) {
		l.temperature = temperature
		l.temperatureSet = true
	}
}

func WithAWSLLMTopP(topP float32) AWSLLMOption {
	return func(l *AWSLLM) {
		l.topP = topP
		l.topPSet = true
	}
}

func WithAWSLLMAdditionalRequestFields(fields any) AWSLLMOption {
	return func(l *AWSLLM) {
		l.additionalFields = fields
	}
}

func WithAWSLLMCacheSystem(cache bool) AWSLLMOption {
	return func(l *AWSLLM) {
		l.cacheSystem = cache
	}
}

func WithAWSLLMCacheTools(cache bool) AWSLLMOption {
	return func(l *AWSLLM) {
		l.cacheTools = cache
	}
}

func NewAWSLLM(ctx context.Context, region string, model string, providerOpts ...AWSLLMOption) (*AWSLLM, error) {
	model = awsLLMModelOrDefault(model)
	region = awsRegionOrDefault(region)

	provider := &AWSLLM{
		model: model,
	}
	for _, opt := range providerOpts {
		opt(provider)
	}
	opts := []func(*config.LoadOptions) error{config.WithRegion(region)}
	if opt := awsCredentialsLoadOption(provider.credentials, provider.credentialsSet); opt != nil {
		opts = append(opts, opt)
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	provider.client = bedrockruntime.NewFromConfig(cfg)
	return provider, nil
}

func awsLLMModelOrDefault(model string) string {
	if model == "" {
		return defaultAWSLLMModel
	}
	return model
}

func awsRegionOrDefault(region string) string {
	if region == "" {
		return defaultAWSRegion
	}
	return region
}

func (l *AWSLLM) Label() string { return "aws.LLM" }
func (l *AWSLLM) Model() string { return l.model }
func (l *AWSLLM) Provider() string {
	return "AWS Bedrock"
}
func (l *AWSLLM) ToolChoice() llm.ToolChoice {
	if l == nil {
		return nil
	}
	return l.toolChoice
}
func (l *AWSLLM) MaxOutputTokens() (int32, bool) {
	if l == nil {
		return 0, false
	}
	return l.maxOutputTokens, l.maxOutputTokensSet
}
func (l *AWSLLM) Temperature() (float32, bool) {
	if l == nil {
		return 0, false
	}
	return l.temperature, l.temperatureSet
}
func (l *AWSLLM) TopP() (float32, bool) {
	if l == nil {
		return 0, false
	}
	return l.topP, l.topPSet
}
func (l *AWSLLM) AdditionalRequestFields() any {
	if l == nil {
		return nil
	}
	return l.additionalFields
}
func (l *AWSLLM) CacheSystem() bool {
	return l != nil && l.cacheSystem
}
func (l *AWSLLM) CacheTools() bool {
	return l != nil && l.cacheTools
}

func (l *AWSLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	if l.client == nil {
		return nil, llm.NewAPIConnectionError("aws bedrock client is not configured")
	}

	options := &llm.ChatOptions{}
	for _, opt := range opts {
		opt(options)
	}
	requestOptions := *options
	if requestOptions.ToolChoice == nil {
		requestOptions.ToolChoice = l.toolChoice
	}
	connectOptions, err := options.EffectiveConnectOptions()
	if err != nil {
		return nil, err
	}
	var cancel context.CancelFunc
	if connectOptions.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, connectOptions.Timeout)
	}

	toolConfig := (*types.ToolConfiguration)(nil)
	if len(requestOptions.Tools) > 0 {
		toolConfig = buildAWSToolConfigWithCache(&requestOptions, l.cacheTools)
	}
	if toolConfig == nil {
		chatCtx = chatCtx.Copy(llm.ChatContextCopyOptions{ExcludeFunctionCall: true})
	}
	if err := validateAWSMessageImages(chatCtx); err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	messages, systemText := buildAWSMessages(chatCtx)

	req := &bedrockruntime.ConverseStreamInput{
		ModelId:  aws.String(l.model),
		Messages: messages,
	}
	if inferenceConfig := l.buildAWSInferenceConfig(options.ExtraParams); inferenceConfig != nil {
		req.InferenceConfig = inferenceConfig
	}
	if fields, ok := options.ExtraParams["additional_request_fields"]; ok {
		req.AdditionalModelRequestFields = document.NewLazyDocument(fields)
	} else if l.additionalFields != nil {
		req.AdditionalModelRequestFields = document.NewLazyDocument(l.additionalFields)
	}

	if systemText != "" {
		req.System = []types.SystemContentBlock{
			&types.SystemContentBlockMemberText{Value: systemText},
		}
		if l.cacheSystem {
			req.System = append(req.System, awsCachePointSystemBlock())
		}
	}

	if toolConfig != nil {
		req.ToolConfig = toolConfig
	}

	chunkStream := newAWSRealtimeQueuedStream[*llm.ChatChunk]()
	stream := &awsLLMStream{
		client:      l.client,
		ctx:         ctx,
		request:     req,
		cancel:      cancel,
		chunks:      chunkStream.Chan(),
		chunkStream: chunkStream,
		errCh:       make(chan error, 1),
	}
	go stream.readLoop()
	return stream, nil
}

func buildAWSInferenceConfig(params map[string]any) *types.InferenceConfiguration {
	if len(params) == 0 {
		return nil
	}
	config := &types.InferenceConfiguration{}
	if maxTokens, ok := awsInt32Param(params, "max_output_tokens"); ok {
		config.MaxTokens = aws.Int32(maxTokens)
	}
	if temperature, ok := awsFloat32Param(params, "temperature"); ok {
		config.Temperature = aws.Float32(temperature)
	}
	if topP, ok := awsFloat32Param(params, "top_p"); ok {
		config.TopP = aws.Float32(topP)
	}
	if config.MaxTokens == nil && config.Temperature == nil && config.TopP == nil {
		return nil
	}
	return config
}

func validateAWSMessageImages(chatCtx *llm.ChatContext) error {
	if chatCtx == nil {
		return nil
	}
	for _, item := range chatCtx.Items {
		msg, ok := item.(*llm.ChatMessage)
		if !ok {
			continue
		}
		for _, content := range msg.Content {
			if content.Image == nil {
				continue
			}
			img, err := llm.SerializeImage(content.Image)
			if err != nil {
				return err
			}
			if img.ExternalURL != "" {
				return fmt.Errorf("external_url is not supported by AWS Bedrock")
			}
		}
	}
	return nil
}

func (l *AWSLLM) buildAWSInferenceConfig(params map[string]any) *types.InferenceConfiguration {
	config := buildAWSInferenceConfig(params)
	if config == nil {
		config = &types.InferenceConfiguration{}
	}
	if config.MaxTokens == nil && l.maxOutputTokensSet {
		config.MaxTokens = aws.Int32(l.maxOutputTokens)
	}
	if config.Temperature == nil && l.temperatureSet {
		config.Temperature = aws.Float32(l.temperature)
	}
	if config.TopP == nil && l.topPSet {
		config.TopP = aws.Float32(l.topP)
	}
	if config.MaxTokens == nil && config.Temperature == nil && config.TopP == nil {
		return nil
	}
	return config
}

func awsInt32Param(params map[string]any, key string) (int32, bool) {
	switch value := params[key].(type) {
	case int:
		return int32(value), true
	case int32:
		return value, true
	case int64:
		return int32(value), true
	case float64:
		return int32(value), true
	case float32:
		return int32(value), true
	default:
		return 0, false
	}
}

func awsFloat32Param(params map[string]any, key string) (float32, bool) {
	switch value := params[key].(type) {
	case float32:
		return value, true
	case float64:
		return float32(value), true
	case int:
		return float32(value), true
	case int32:
		return float32(value), true
	case int64:
		return float32(value), true
	default:
		return 0, false
	}
}

func buildAWSToolConfig(options *llm.ChatOptions) *types.ToolConfiguration {
	return buildAWSToolConfigWithCache(options, false)
}

func buildAWSToolConfigWithCache(options *llm.ChatOptions, cacheTools bool) *types.ToolConfiguration {
	if len(options.Tools) == 0 || options.ToolChoice == "none" {
		return nil
	}

	toolSpecs := make([]types.Tool, 0, len(options.Tools))
	for _, t := range options.Tools {
		doc := document.NewLazyDocument(llm.ToolParameters(t))
		spec := types.ToolSpecification{
			Name: aws.String(t.Name()),
			InputSchema: &types.ToolInputSchemaMemberJson{
				Value: doc,
			},
		}
		if description := t.Description(); description != "" {
			spec.Description = aws.String(description)
		}
		toolSpecs = append(toolSpecs, &types.ToolMemberToolSpec{
			Value: spec,
		})
	}
	if cacheTools {
		toolSpecs = append(toolSpecs, awsCachePointToolBlock())
	}

	return &types.ToolConfiguration{
		Tools:      toolSpecs,
		ToolChoice: buildAWSToolChoice(options.ToolChoice),
	}
}

func awsCachePointSystemBlock() types.SystemContentBlock {
	return &types.SystemContentBlockMemberCachePoint{
		Value: types.CachePointBlock{Type: types.CachePointTypeDefault},
	}
}

func awsCachePointToolBlock() types.Tool {
	return &types.ToolMemberCachePoint{
		Value: types.CachePointBlock{Type: types.CachePointTypeDefault},
	}
}

func buildAWSToolChoice(choice llm.ToolChoice) types.ToolChoice {
	switch tc := choice.(type) {
	case string:
		switch tc {
		case "auto":
			return &types.ToolChoiceMemberAuto{Value: types.AutoToolChoice{}}
		case "required":
			return &types.ToolChoiceMemberAny{Value: types.AnyToolChoice{}}
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
		return &types.ToolChoiceMemberTool{
			Value: types.SpecificToolChoice{Name: aws.String(name)},
		}
	}
	return nil
}

type awsLLMStream struct {
	client       awsLLMClient
	ctx          context.Context
	request      *bedrockruntime.ConverseStreamInput
	stream       *bedrockruntime.ConverseStreamEventStream
	requestID    string
	cancel       context.CancelFunc
	closed       bool
	emittedChunk bool
	toolCallID   string
	toolName     string
	toolNameSet  bool
	toolArgs     string
	chunks       <-chan *llm.ChatChunk
	chunkStream  *awsRealtimeQueuedStream[*llm.ChatChunk]
	errCh        chan error
}

func buildAWSMessages(chatCtx *llm.ChatContext) ([]types.Message, string) {
	messages := make([]types.Message, 0, len(chatCtx.Items))
	var systemText string
	var currentRole *types.ConversationRole
	currentContent := make([]types.ContentBlock, 0)

	flush := func() {
		if currentRole == nil || len(currentContent) == 0 {
			return
		}
		messages = append(messages, types.Message{
			Role:    *currentRole,
			Content: currentContent,
		})
		currentContent = nil
	}

	appendBlock := func(role types.ConversationRole, blocks ...types.ContentBlock) {
		if currentRole == nil || *currentRole != role {
			flush()
			currentRole = &role
			currentContent = make([]types.ContentBlock, 0, len(blocks))
		}
		currentContent = append(currentContent, blocks...)
	}

	for _, group := range groupAWSChatItems(convertAWSMidConversationInstructions(chatCtx.Items)) {
		for _, item := range group.flatten() {
			switch msg := item.(type) {
			case *llm.ChatMessage:
				if msg.Role == llm.ChatRoleSystem {
					if text := msg.TextContent(); text != "" {
						if systemText != "" {
							systemText += "\n"
						}
						systemText += text
					}
					continue
				}
				role := types.ConversationRoleUser
				if msg.Role == llm.ChatRoleAssistant {
					role = types.ConversationRoleAssistant
				}
				blocks := awsMessageContentBlocks(msg)
				if len(blocks) > 0 {
					appendBlock(role, blocks...)
				}
			case *llm.FunctionCall:
				appendBlock(types.ConversationRoleAssistant, awsToolUseBlock(msg))
			case *llm.FunctionCallOutput:
				appendBlock(types.ConversationRoleUser, awsToolResultBlock(msg))
			}
		}
	}
	flush()

	if len(messages) == 0 || messages[0].Role != types.ConversationRoleUser {
		messages = append([]types.Message{
			{
				Role:    types.ConversationRoleUser,
				Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "(empty)"}},
			},
		}, messages...)
	}

	return messages, systemText
}

func convertAWSMidConversationInstructions(items []llm.ChatItem) []llm.ChatItem {
	converted := make([]llm.ChatItem, 0, len(items))
	firstSystemSeen := false
	for _, item := range items {
		msg, ok := item.(*llm.ChatMessage)
		if !ok || (msg.Role != llm.ChatRoleSystem && msg.Role != llm.ChatRoleDeveloper) {
			converted = append(converted, item)
			continue
		}
		text := msg.TextContent()
		if firstSystemSeen && text != "" {
			converted = append(converted, &llm.ChatMessage{
				ID:        msg.ID,
				Role:      llm.ChatRoleUser,
				Content:   []llm.ChatContent{{Text: fmt.Sprintf("<instructions>\n%s\n</instructions>", text)}},
				CreatedAt: msg.CreatedAt,
			})
			continue
		}
		firstSystemSeen = true
		converted = append(converted, item)
	}
	return converted
}

func awsMessageContentBlocks(msg *llm.ChatMessage) []types.ContentBlock {
	blocks := make([]types.ContentBlock, 0, len(msg.Content))
	for _, c := range msg.Content {
		if c.Text != "" {
			blocks = append(blocks, &types.ContentBlockMemberText{Value: c.Text})
		}
		if c.Image != nil {
			if block := awsImageBlock(c.Image); block != nil {
				blocks = append(blocks, block)
			}
		}
	}
	return blocks
}

func awsImageBlock(image *llm.ImageContent) types.ContentBlock {
	img, err := llm.SerializeImage(image)
	if err != nil || img.ExternalURL != "" {
		return nil
	}
	return &types.ContentBlockMemberImage{
		Value: types.ImageBlock{
			Format: types.ImageFormatJpeg,
			Source: &types.ImageSourceMemberBytes{Value: img.DataBytes},
		},
	}
}

func awsToolUseBlock(fc *llm.FunctionCall) types.ContentBlock {
	var args map[string]interface{}
	_ = json.Unmarshal([]byte(fc.Arguments), &args)
	if args == nil {
		args = map[string]interface{}{}
	}

	return &types.ContentBlockMemberToolUse{
		Value: types.ToolUseBlock{
			ToolUseId: aws.String(fc.CallID),
			Name:      aws.String(fc.Name),
			Input:     document.NewLazyDocument(args),
		},
	}
}

func awsToolResultBlock(fco *llm.FunctionCallOutput) types.ContentBlock {
	return &types.ContentBlockMemberToolResult{
		Value: types.ToolResultBlock{
			ToolUseId: aws.String(fco.CallID),
			Status:    types.ToolResultStatusSuccess,
			Content: []types.ToolResultContentBlock{
				&types.ToolResultContentBlockMemberText{
					Value: fco.Output,
				},
			},
		},
	}
}

type awsChatItemGroup struct {
	message     *llm.ChatMessage
	toolCalls   []*llm.FunctionCall
	toolOutputs []*llm.FunctionCallOutput
}

func groupAWSChatItems(items []llm.ChatItem) []*awsChatItemGroup {
	groups := make([]*awsChatItemGroup, 0)
	groupsByID := make(map[string]*awsChatItemGroup)
	toolOutputs := make([]*llm.FunctionCallOutput, 0)

	addToGroup := func(groupID string, item llm.ChatItem) {
		group := groupsByID[groupID]
		if group == nil {
			group = &awsChatItemGroup{}
			groupsByID[groupID] = group
			groups = append(groups, group)
		}
		group.add(item)
	}

	for _, item := range items {
		switch it := item.(type) {
		case *llm.ChatMessage:
			if it.Role == llm.ChatRoleAssistant {
				addToGroup(awsGroupID(it.ID, nil), it)
			} else {
				addToGroup(it.ID, it)
			}
		case *llm.FunctionCall:
			addToGroup(awsGroupID(it.ID, it.GroupID), it)
		case *llm.FunctionCallOutput:
			toolOutputs = append(toolOutputs, it)
		}
	}

	groupsByCallID := make(map[string]*awsChatItemGroup)
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

func (g *awsChatItemGroup) add(item llm.ChatItem) {
	switch it := item.(type) {
	case *llm.ChatMessage:
		g.message = it
	case *llm.FunctionCall:
		g.toolCalls = append(g.toolCalls, it)
	case *llm.FunctionCallOutput:
		g.toolOutputs = append(g.toolOutputs, it)
	}
}

func (g *awsChatItemGroup) flatten() []llm.ChatItem {
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

func (g *awsChatItemGroup) removeInvalidToolItems() {
	if len(g.toolCalls) == len(g.toolOutputs) {
		return
	}

	callIDs := make(map[string]struct{}, len(g.toolCalls))
	outputIDs := make(map[string]struct{}, len(g.toolOutputs))
	for _, toolCall := range g.toolCalls {
		callIDs[toolCall.CallID] = struct{}{}
	}
	for _, toolOutput := range g.toolOutputs {
		outputIDs[toolOutput.CallID] = struct{}{}
	}

	validCalls := make([]*llm.FunctionCall, 0, len(g.toolCalls))
	for _, toolCall := range g.toolCalls {
		if _, ok := outputIDs[toolCall.CallID]; ok {
			validCalls = append(validCalls, toolCall)
		}
	}

	validOutputs := make([]*llm.FunctionCallOutput, 0, len(g.toolOutputs))
	for _, toolOutput := range g.toolOutputs {
		if _, ok := callIDs[toolOutput.CallID]; ok {
			validOutputs = append(validOutputs, toolOutput)
		}
	}

	g.toolCalls = validCalls
	g.toolOutputs = validOutputs
}

func awsGroupID(itemID string, groupID *string) string {
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

func (s *awsLLMStream) Next() (*llm.ChatChunk, error) {
	if s.closed {
		return nil, io.EOF
	}
	if s.chunks != nil {
		chunk, ok := <-s.chunks
		if ok {
			return chunk, nil
		}
		if s.closed {
			return nil, io.EOF
		}
		select {
		case err := <-s.errCh:
			return nil, err
		default:
			return nil, io.EOF
		}
	}

	return s.nextFromProvider()
}

func (s *awsLLMStream) readLoop() {
	defer s.chunkStream.Close()
	if err := s.open(); err != nil {
		select {
		case s.errCh <- err:
		default:
		}
		return
	}
	for {
		chunk, err := s.nextFromProvider()
		if err != nil {
			if err != io.EOF {
				select {
				case s.errCh <- err:
				default:
				}
			}
			return
		}
		if chunk != nil && !s.chunkStream.Send(chunk) {
			return
		}
	}
}

func (s *awsLLMStream) open() error {
	if s.stream != nil {
		return nil
	}
	if s.client == nil {
		return llm.NewAPIConnectionError("aws bedrock client is not configured")
	}
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	out, err := s.client.ConverseStream(ctx, s.request)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return llm.NewAPITimeoutError("")
		}
		var responseErr *smithyhttp.ResponseError
		if errors.As(err, &responseErr) {
			var requestID string
			var statusCode int
			if responseErr.Response != nil && responseErr.Response.Response != nil {
				statusCode = responseErr.Response.Response.StatusCode
				requestID = responseErr.Response.Response.Header.Get("x-amzn-requestid")
			}
			return llm.NewAPIStatusErrorWithRetryable(
				fmt.Sprintf("aws bedrock llm: error generating content: %v", err),
				statusCode,
				requestID,
				nil,
				false,
			)
		}
		return llm.NewAPIConnectionError(fmt.Sprintf("AWS Bedrock LLM chat failed: %v", err))
	}
	s.requestID, _ = awsmiddleware.GetRequestIDMetadata(out.ResultMetadata)
	s.stream = out.GetStream()
	return nil
}

func (s *awsLLMStream) nextFromProvider() (*llm.ChatChunk, error) {
	for {
		event := <-s.stream.Events()
		if event == nil {
			if err := s.stream.Err(); err != nil {
				s.closeContext()
				if errors.Is(err, context.DeadlineExceeded) {
					return nil, llm.NewAPITimeoutErrorWithRetryable("", !s.emittedChunk)
				}
				return nil, llm.NewAPIConnectionErrorWithRetryable(fmt.Sprintf("AWS Bedrock LLM stream failed: %v", err), !s.emittedChunk)
			}
			s.closeContext()
			return nil, io.EOF
		}

		switch v := event.(type) {
		case *types.ConverseStreamOutputMemberContentBlockDelta:
			if textDelta, ok := v.Value.Delta.(*types.ContentBlockDeltaMemberText); ok {
				s.emittedChunk = true
				return &llm.ChatChunk{
					ID: s.requestID,
					Delta: &llm.ChoiceDelta{
						Role:    llm.ChatRoleAssistant,
						Content: textDelta.Value,
					},
				}, nil
			}
			if toolDelta, ok := v.Value.Delta.(*types.ContentBlockDeltaMemberToolUse); ok {
				if s.toolCallID == "" {
					s.closeContext()
					return nil, llm.NewAPIConnectionErrorWithRetryable("AWS Bedrock LLM stream failed: toolUse delta received before toolUse start", !s.emittedChunk)
				}
				if toolDelta.Value.Input == nil {
					s.closeContext()
					return nil, llm.NewAPIConnectionErrorWithRetryable("AWS Bedrock LLM stream failed: malformed toolUse delta missing input", !s.emittedChunk)
				}
				s.toolArgs += aws.ToString(toolDelta.Value.Input)
				continue
			}
		case *types.ConverseStreamOutputMemberContentBlockStart:
			if toolStart, ok := v.Value.Start.(*types.ContentBlockStartMemberToolUse); ok {
				if toolStart.Value.ToolUseId == nil || toolStart.Value.Name == nil {
					s.closeContext()
					return nil, llm.NewAPIConnectionErrorWithRetryable("AWS Bedrock LLM stream failed: malformed toolUse start missing required fields", !s.emittedChunk)
				}
				s.toolCallID = aws.ToString(toolStart.Value.ToolUseId)
				s.toolName = aws.ToString(toolStart.Value.Name)
				s.toolNameSet = true
				s.toolArgs = ""
				continue
			}
		case *types.ConverseStreamOutputMemberContentBlockStop:
			if s.toolCallID != "" {
				if !s.toolNameSet {
					s.toolCallID, s.toolName, s.toolArgs, s.toolNameSet = "", "", "", false
					continue
				}
				s.emittedChunk = true
				chunk := &llm.ChatChunk{
					ID: s.requestID,
					Delta: &llm.ChoiceDelta{
						Role: llm.ChatRoleAssistant,
						ToolCalls: []llm.FunctionToolCall{{
							CallID:    s.toolCallID,
							Name:      s.toolName,
							Type:      "function",
							Arguments: s.toolArgs,
						}},
					},
				}
				s.toolCallID, s.toolName, s.toolArgs, s.toolNameSet = "", "", "", false
				return chunk, nil
			}
		case *types.ConverseStreamOutputMemberMetadata:
			if v.Value.Usage != nil {
				s.emittedChunk = true
				return &llm.ChatChunk{
					ID: s.requestID,
					Usage: &llm.CompletionUsage{
						PromptTokens:       int(aws.ToInt32(v.Value.Usage.InputTokens)),
						CompletionTokens:   int(aws.ToInt32(v.Value.Usage.OutputTokens)),
						TotalTokens:        int(aws.ToInt32(v.Value.Usage.TotalTokens)),
						PromptCachedTokens: int(aws.ToInt32(v.Value.Usage.CacheReadInputTokens)),
					},
				}, nil
			}
		case *types.ConverseStreamOutputMemberMessageStop:
			continue
		}
	}
}

func (s *awsLLMStream) Close() error {
	s.closeContext()
	s.closed = true
	if s.chunkStream != nil {
		s.chunkStream.Close()
	}
	if s.stream == nil {
		return nil
	}
	s.stream.Close()
	return nil
}

func (s *awsLLMStream) closeContext() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}
