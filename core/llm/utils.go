package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/cavos-io/rtp-agent/library/utils/images"
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
	serialized := &SerializedImage{
		InferenceDetail: imageInferenceDetailOrDefault(image.InferenceDetail),
		MIMEType:        image.MimeType,
	}
	if frame, ok := image.Image.(*images.VideoFrame); ok {
		opts := images.NewEncodeOptions()
		if image.InferenceWidth != nil && image.InferenceHeight != nil {
			opts.Width = *image.InferenceWidth
			opts.Height = *image.InferenceHeight
			opts.Strategy = "scale_aspect_fit"
		}
		data, err := images.Encode(frame, opts)
		if err != nil {
			return nil, fmt.Errorf("encode video frame image: %w", err)
		}
		serialized.MIMEType = "image/jpeg"
		serialized.DataBytes = data
		return serialized, nil
	}

	imageString, ok := image.Image.(string)
	if !ok || imageString == "" {
		return nil, fmt.Errorf("%s image type", "Unsupported")
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
		return nil, fmt.Errorf("%s mime_type %s. Must be jpeg, png, webp, or gif", "Unsupported", mimeType)
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
		if repaired == "" || repaired == jsonArguments {
			return nil, fmt.Errorf("could not parse function arguments as JSON: %w: %.200s", err, jsonArguments)
		}
		if retryErr := json.Unmarshal([]byte(repaired), &value); retryErr != nil {
			return nil, fmt.Errorf("could not parse function arguments as JSON: %w: %.200s", err, jsonArguments)
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
		return nil, fmt.Errorf("expected dict from function arguments, got %s: %.200s", functionArgumentTypeName(value), jsonArguments)
	}
	return args, nil
}

func functionArgumentTypeName(value any) string {
	switch number := value.(type) {
	case []any:
		return "list"
	case string:
		return "str"
	case bool:
		return "bool"
	case float64:
		if number == float64(int64(number)) {
			return "int"
		}
		return "float"
	default:
		return fmt.Sprintf("%T", value)
	}
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
		outputString = functionOutputString(output)
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

func MakeToolOutput(fncCall FunctionCall, output any, exception error) FunctionCallResult {
	return MakeFunctionCallOutput(fncCall, output, exception)
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
		//lint:ignore ST1005 match LiveKit Agents raw ValueError text
		err := fmt.Errorf("Unknown function: %s", toolCall.Name)
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

	parsedArgs, err := ParseFunctionArguments(args)
	if err != nil {
		return MakeToolOutput(fncCall, nil, NewToolError(fmt.Sprintf("Error parsing arguments for `%s`: %s", toolCall.Name, err.Error())))
	}
	encodedArgs, err := json.Marshal(parsedArgs)
	if err != nil {
		return MakeToolOutput(fncCall, nil, err)
	}
	args = string(encodedArgs)
	fncCall.Arguments = args

	output, err := tool.Execute(ctx, args)
	result := MakeToolOutput(fncCall, output, err)
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

func functionOutputString(value any) string {
	if isFalsyFunctionOutput(value) {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return functionOutputRepr(value)
}

func functionOutputRepr(value any) string {
	if value == nil {
		return "None"
	}
	switch v := value.(type) {
	case string:
		return functionOutputStringRepr(v)
	case bool:
		if v {
			return "True"
		}
		return "False"
	case float32:
		return functionOutputFloatRepr(float64(v), 32)
	case float64:
		return functionOutputFloatRepr(v, 64)
	case complex64:
		return functionOutputComplexRepr(complex128(v), 32)
	case complex128:
		return functionOutputComplexRepr(v, 64)
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, functionOutputRepr(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, functionOutputRepr(key)+": "+functionOutputRepr(v[key]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Array:
		parts := make([]string, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			parts = append(parts, functionOutputRepr(rv.Index(i).Interface()))
		}
		if rv.Len() == 1 {
			return "(" + parts[0] + ",)"
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case reflect.Slice:
		parts := make([]string, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			parts = append(parts, functionOutputRepr(rv.Index(i).Interface()))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case reflect.Map:
		parts := make([]string, 0, rv.Len())
		for _, key := range rv.MapKeys() {
			parts = append(parts, functionOutputRepr(key.Interface())+": "+functionOutputRepr(rv.MapIndex(key).Interface()))
		}
		sort.Strings(parts)
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprint(value)
}

func functionOutputFloatRepr(value float64, bitSize int) string {
	switch {
	case math.IsInf(value, 1):
		return "inf"
	case math.IsInf(value, -1):
		return "-inf"
	case math.IsNaN(value):
		return "nan"
	case value == 0:
		if math.Signbit(value) {
			return "-0.0"
		}
		return "0.0"
	default:
		text := strconv.FormatFloat(value, 'g', -1, bitSize)
		if math.Trunc(value) == value && !strings.ContainsAny(text, ".eE") {
			text += ".0"
		}
		return text
	}
}

func functionOutputStringRepr(value string) string {
	quote := "'"
	if strings.Contains(value, "'") && !strings.Contains(value, `"`) {
		quote = `"`
	}
	var escaped strings.Builder
	for _, r := range value {
		switch {
		case r == '\\':
			escaped.WriteString(`\\`)
		case r == '\n':
			escaped.WriteString(`\n`)
		case r == '\r':
			escaped.WriteString(`\r`)
		case r == '\t':
			escaped.WriteString(`\t`)
		case r < 0x20:
			escaped.WriteString(fmt.Sprintf(`\x%02x`, r))
		case r < 0x100 && !unicode.IsPrint(r):
			escaped.WriteString(fmt.Sprintf(`\x%02x`, r))
		case r < 0x10000 && !unicode.IsPrint(r):
			escaped.WriteString(fmt.Sprintf(`\u%04x`, r))
		case !unicode.IsPrint(r):
			escaped.WriteString(fmt.Sprintf(`\U%08x`, r))
		case quote == "'" && r == '\'':
			escaped.WriteString(`\'`)
		case quote == `"` && r == '"':
			escaped.WriteString(`\"`)
		default:
			escaped.WriteRune(r)
		}
	}
	return quote + escaped.String() + quote
}

func functionOutputComplexRepr(value complex128, bitSize int) string {
	realPart := real(value)
	imagPart := imag(value)
	realText := functionOutputComplexFloatRepr(realPart, bitSize)
	imagText := functionOutputComplexFloatRepr(imagPart, bitSize)
	if realPart == 0 && !math.Signbit(realPart) {
		return imagText + "j"
	}
	if math.Signbit(imagPart) {
		return "(" + realText + imagText + "j)"
	}
	return "(" + realText + "+" + imagText + "j)"
}

func functionOutputComplexFloatRepr(value float64, bitSize int) string {
	switch {
	case math.IsInf(value, 1):
		return "inf"
	case math.IsInf(value, -1):
		return "-inf"
	case math.IsNaN(value):
		return "nan"
	case value == 0 && math.Signbit(value):
		return "-0"
	default:
		return strconv.FormatFloat(value, 'g', -1, bitSize)
	}
}

func isFalsyFunctionOutput(value any) bool {
	if value == nil {
		return true
	}
	switch v := value.(type) {
	case string:
		return v == ""
	case bool:
		return !v
	case int:
		return v == 0
	case int8:
		return v == 0
	case int16:
		return v == 0
	case int32:
		return v == 0
	case int64:
		return v == 0
	case uint:
		return v == 0
	case uint8:
		return v == 0
	case uint16:
		return v == 0
	case uint32:
		return v == 0
	case uint64:
		return v == 0
	case float32:
		return v == 0
	case float64:
		return v == 0
	case complex64:
		return v == 0
	case complex128:
		return v == 0
	}

	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Array, reflect.Slice, reflect.Map:
		return v.Len() == 0
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
