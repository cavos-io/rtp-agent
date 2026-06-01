package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"
	"time"
)

type DiffOps struct {
	ToRemove []string
	ToCreate [][2]*string // [previous_item_id, id]
	ToUpdate [][2]*string // [previous_item_id, id]
}

const (
	thinkTagStart = "<think>"
	thinkTagEnd   = "</think>"
)

var templateTokenPatterns = []*regexp.Regexp{
	regexp.MustCompile(`<\|[^<>|]{0,40}\|>`),
	regexp.MustCompile(`<\|[^<>a-zA-Z0-9_]{0,10}`),
	regexp.MustCompile(`[^<>a-zA-Z0-9_]{0,10}\|>`),
	regexp.MustCompile(`<(?:start|end)_of_turn>`),
}

var trailingCommaPattern = regexp.MustCompile(`,\s*([}\]])`)

var unquotedObjectKeyPattern = regexp.MustCompile(`([,{]\s*)([A-Za-z_][A-Za-z0-9_-]*)(\s*:)`)

var singleQuotedStringPattern = regexp.MustCompile(`'([^'\\]*)'`)

type SerializedImage struct {
	InferenceDetail string
	MIMEType        string
	DataBytes       []byte
	ExternalURL     string
}

type FunctionCallResult struct {
	FncCall    FunctionCall
	FncCallOut *FunctionCallOutput
	RawOutput  any
	RawError   error
}

func SerializeImage(image *ImageContent) (*SerializedImage, error) {
	if image == nil {
		return nil, fmt.Errorf("image content is nil")
	}
	imageString, ok := image.Image.(string)
	if !ok || imageString == "" {
		return nil, fmt.Errorf("unsupported image type")
	}
	serialized := &SerializedImage{
		InferenceDetail: imageInferenceDetailOrDefault(image.InferenceDetail),
		MIMEType:        image.MimeType,
	}
	if !strings.HasPrefix(imageString, "data:") {
		serialized.ExternalURL = imageString
		return serialized, nil
	}

	header, encodedData, ok := strings.Cut(imageString, ",")
	if !ok {
		return nil, fmt.Errorf("invalid data URL image")
	}
	headerMIME := strings.TrimPrefix(strings.Split(header, ";")[0], "data:")
	mimeType := image.MimeType
	if mimeType == "" {
		mimeType = headerMIME
	}
	if !isSupportedImageMIMEType(mimeType) {
		return nil, fmt.Errorf("unsupported mime_type %s", mimeType)
	}
	data, err := base64.StdEncoding.DecodeString(encodedData)
	if err != nil {
		return nil, fmt.Errorf("decode data URL image: %w", err)
	}
	serialized.MIMEType = mimeType
	serialized.DataBytes = data
	return serialized, nil
}

func isSupportedImageMIMEType(mimeType string) bool {
	switch mimeType {
	case "image/jpeg", "image/png", "image/webp", "image/gif":
		return true
	default:
		return false
	}
}

func StripThinkingTokens(content string, thinking *bool) (string, bool) {
	if thinking == nil {
		return content, true
	}
	if *thinking {
		idx := strings.Index(content, thinkTagEnd)
		if idx >= 0 {
			*thinking = false
			return content[idx+len(thinkTagEnd):], true
		}
		return "", false
	}

	idx := strings.Index(content, thinkTagStart)
	if idx >= 0 {
		*thinking = true
		return content[idx+len(thinkTagStart):], true
	}
	return content, true
}

func ParseFunctionArguments(jsonArguments string) (map[string]any, error) {
	var value any
	if err := json.Unmarshal([]byte(jsonArguments), &value); err != nil {
		repaired := repairFunctionArguments(jsonArguments)
		if repaired == jsonArguments {
			return nil, fmt.Errorf("could not parse function arguments as JSON: %w", err)
		}
		if retryErr := json.Unmarshal([]byte(repaired), &value); retryErr != nil {
			return nil, fmt.Errorf("could not parse function arguments as JSON: %w", err)
		}
		value = cleanRepairedFunctionArguments(value)
	}

	for {
		nested, ok := value.(string)
		if !ok {
			break
		}
		if err := json.Unmarshal([]byte(nested), &value); err != nil {
			return nil, fmt.Errorf("function arguments decoded to a non-JSON string: %.200s", nested)
		}
	}

	if value == nil {
		return map[string]any{}, nil
	}
	args, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected object from function arguments, got %T", value)
	}
	return args, nil
}

func repairFunctionArguments(value string) string {
	out := value
	for _, pattern := range templateTokenPatterns {
		out = pattern.ReplaceAllString(out, "")
	}
	out = singleQuotedStringPattern.ReplaceAllString(out, `"$1"`)
	out = unquotedObjectKeyPattern.ReplaceAllString(out, `${1}"${2}"${3}`)
	out = closeUnbalancedJSONContainers(out)
	out = trailingCommaPattern.ReplaceAllString(out, "$1")
	return strings.TrimSpace(out)
}

func closeUnbalancedJSONContainers(value string) string {
	stack := make([]byte, 0)
	inString := false
	escaped := false

	for i := 0; i < len(value); i++ {
		ch := value[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) == 0 || stack[len(stack)-1] != ch {
				return value
			}
			stack = stack[:len(stack)-1]
		}
	}

	if inString || len(stack) == 0 {
		return value
	}
	var b strings.Builder
	b.Grow(len(value) + len(stack))
	b.WriteString(value)
	for i := len(stack) - 1; i >= 0; i-- {
		b.WriteByte(stack[i])
	}
	return b.String()
}

func cleanRepairedFunctionArguments(value any) any {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		cleaned := make([]any, 0, len(v))
		for _, item := range v {
			item = cleanRepairedFunctionArguments(item)
			if item == "" || item == nil {
				continue
			}
			cleaned = append(cleaned, item)
		}
		return cleaned
	case map[string]any:
		cleaned := make(map[string]any, len(v))
		for key, item := range v {
			cleaned[key] = cleanRepairedFunctionArguments(item)
		}
		return cleaned
	default:
		return value
	}
}

func MakeFunctionCallOutput(fncCall FunctionCall, output any, exception error) FunctionCallResult {
	if outputErr, ok := output.(error); ok {
		exception = outputErr
		output = nil
	}

	var toolErr ToolError
	if errors.As(exception, &toolErr) {
		return FunctionCallResult{
			FncCall: fncCall,
			FncCallOut: &FunctionCallOutput{
				CallID:    fncCall.CallID,
				Name:      fncCall.Name,
				Output:    toolErr.Message,
				IsError:   true,
				CreatedAt: time.Now(),
			},
			RawOutput: output,
			RawError:  exception,
		}
	}

	var stopResponse StopResponse
	if errors.As(exception, &stopResponse) {
		return FunctionCallResult{
			FncCall:   fncCall,
			RawOutput: output,
			RawError:  exception,
		}
	}

	if exception != nil {
		return FunctionCallResult{
			FncCall: fncCall,
			FncCallOut: &FunctionCallOutput{
				CallID:    fncCall.CallID,
				Name:      fncCall.Name,
				Output:    "An internal error occurred",
				IsError:   true,
				CreatedAt: time.Now(),
			},
			RawOutput: output,
			RawError:  exception,
		}
	}

	if !isValidFunctionOutput(output) {
		return FunctionCallResult{
			FncCall:   fncCall,
			RawOutput: output,
		}
	}

	outputString := ""
	if output != nil {
		outputString = fmt.Sprint(output)
	}

	return FunctionCallResult{
		FncCall: fncCall,
		FncCallOut: &FunctionCallOutput{
			CallID:    fncCall.CallID,
			Name:      fncCall.Name,
			Output:    outputString,
			IsError:   false,
			CreatedAt: time.Now(),
		},
		RawOutput: output,
	}
}

func CollectStream(stream LLMStream) (*CollectedResponse, error) {
	if stream == nil {
		return nil, fmt.Errorf("llm stream is nil")
	}
	defer stream.Close()

	var textParts []string
	var toolCalls []FunctionToolCall
	var usage *CompletionUsage
	extra := make(map[string]any)

	for {
		chunk, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if chunk == nil {
			continue
		}
		if chunk.Delta != nil {
			if chunk.Delta.Content != "" {
				textParts = append(textParts, chunk.Delta.Content)
			}
			if len(chunk.Delta.ToolCalls) > 0 {
				toolCalls = append(toolCalls, chunk.Delta.ToolCalls...)
			}
			for key, value := range chunk.Delta.Extra {
				extra[key] = value
			}
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
	}

	return &CollectedResponse{
		Text:      strings.TrimSpace(strings.Join(textParts, "")),
		ToolCalls: toolCalls,
		Usage:     usage,
		Extra:     extra,
	}, nil
}

type TextStream struct {
	stream LLMStream
	closed bool
}

func NewTextStream(stream LLMStream) (*TextStream, error) {
	if stream == nil {
		return nil, fmt.Errorf("llm stream is nil")
	}
	return &TextStream{stream: stream}, nil
}

func (s *TextStream) Next() (string, error) {
	if s == nil || s.stream == nil {
		return "", fmt.Errorf("llm text stream is nil")
	}
	for {
		chunk, err := s.stream.Next()
		if err != nil {
			_ = s.Close()
			return "", err
		}
		if chunk == nil || chunk.Delta == nil || chunk.Delta.Content == "" {
			continue
		}
		return chunk.Delta.Content, nil
	}
}

func (s *TextStream) Close() error {
	if s == nil || s.stream == nil || s.closed {
		return nil
	}
	s.closed = true
	return s.stream.Close()
}

func ExecuteFunctionCall(ctx context.Context, toolCall *FunctionToolCall, toolCtx *ToolContext) FunctionCallResult {
	args := toolCall.Arguments
	if args == "" {
		args = "{}"
	}
	fncCall := FunctionCall{
		CallID:    toolCall.CallID,
		Name:      toolCall.Name,
		Arguments: args,
		Extra:     toolCall.Extra,
		CreatedAt: time.Now(),
	}

	tool := toolCtx.GetFunctionTool(toolCall.Name)
	if tool == nil {
		err := fmt.Errorf("unknown function: %s", toolCall.Name)
		return FunctionCallResult{
			FncCall: fncCall,
			FncCallOut: &FunctionCallOutput{
				CallID:    toolCall.CallID,
				Name:      toolCall.Name,
				Output:    fmt.Sprintf("Unknown function: %s", toolCall.Name),
				IsError:   true,
				CreatedAt: time.Now(),
			},
			RawError: err,
		}
	}

	output, err := tool.Execute(ctx, args)
	result := MakeFunctionCallOutput(fncCall, output, err)
	if result.FncCallOut != nil && result.FncCallOut.CreatedAt.IsZero() {
		result.FncCallOut.CreatedAt = time.Now()
	}
	return result
}

func isValidFunctionOutput(value any) bool {
	if value == nil {
		return true
	}
	switch value.(type) {
	case string, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, bool, complex64, complex128:
		return true
	}

	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Array, reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if !isValidFunctionOutput(v.Index(i).Interface()) {
				return false
			}
		}
		return true
	case reflect.Map:
		for _, key := range v.MapKeys() {
			if !isValidFunctionOutput(key.Interface()) || !isValidFunctionOutput(v.MapIndex(key).Interface()) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func ComputeChatCtxDiff(oldCtx, newCtx *ChatContext) *DiffOps {
	oldIDs := make([]string, len(oldCtx.Items))
	for i, item := range oldCtx.Items {
		oldIDs[i] = item.GetID()
	}

	newIDs := make([]string, len(newCtx.Items))
	for i, item := range newCtx.Items {
		newIDs[i] = item.GetID()
	}

	lcs := computeLCS(oldIDs, newIDs)
	lcsSet := make(map[string]struct{})
	for _, id := range lcs {
		lcsSet[id] = struct{}{}
	}

	diff := &DiffOps{}
	for _, id := range oldIDs {
		if _, ok := lcsSet[id]; !ok {
			diff.ToRemove = append(diff.ToRemove, id)
		}
	}

	var prevID *string
	for i, item := range newCtx.Items {
		id := item.GetID()
		if _, ok := lcsSet[id]; !ok {
			diff.ToCreate = append(diff.ToCreate, [2]*string{prevID, &id})
		} else {
			// Deep comparison for updates
			var oldItem, newItem ChatItem
			for _, o := range oldCtx.Items {
				if o.GetID() == id {
					oldItem = o
					break
				}
			}
			newItem = item
			if oldItem != nil && newItem != nil {
				if oMsg, ok := oldItem.(*ChatMessage); ok {
					if nMsg, ok := newItem.(*ChatMessage); ok {
						if oMsg.TextContent() != nMsg.TextContent() {
							diff.ToUpdate = append(diff.ToUpdate, [2]*string{prevID, &id})
						}
					}
				}
			}
		}
		newID := newIDs[i]
		prevID = &newID
	}

	return diff
}

func computeLCS(a, b []string) []string {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}

	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}

	var lcs []string
	i, j := n, m
	for i > 0 && j > 0 {
		if a[i-1] == b[j-1] {
			lcs = append([]string{a[i-1]}, lcs...)
			i--
			j--
		} else if dp[i-1][j] > dp[i][j-1] {
			i--
		} else {
			j--
		}
	}
	return lcs
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
