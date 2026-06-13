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

var unquotedStringValuePattern = regexp.MustCompile(`(:\s*)([A-Za-z_][A-Za-z0-9_-]*)(\s*[,}\]])`)

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
	out = quoteExtendedBareStringValues(out)
	out = quoteExtendedBareArrayStringValues(out)
	out = stripJSONComments(out)
	out = escapeStringControlCharacters(out)
	out = normalizePythonBooleanLiterals(out)
	out = normalizeNonstandardNumberLiterals(out)
	out = normalizeTupleLikeArrays(out)
	out = normalizeAlternateSeparators(out)
	out = dropEllipsisPlaceholders(out)
	out = extractJSONObjectFromSurroundingText(out)
	out = normalizeSingleQuotedStrings(out)
	out = normalizeDuplicateValueSeparators(out)
	out = insertMissingColonBetweenQuotedKeyAndString(out)
	out = insertMissingObjectCommas(out)
	out = unquotedObjectKeyPattern.ReplaceAllString(out, `${1}"${2}"${3}`)
	out = quoteUnquotedStringValues(out)
	out = insertMissingArrayCommas(out)
	out = quoteBareArrayStringValues(out)
	out = closeUnbalancedJSONContainers(out)
	out = collapseDuplicateCommas(out)
	out = trailingCommaPattern.ReplaceAllString(out, "$1")
	return strings.TrimSpace(out)
}

func collapseDuplicateCommas(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	inString := false
	escaped := false
	lastSignificant := byte(0)

	for i := 0; i < len(value); i++ {
		ch := value[i]
		if inString {
			b.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
				lastSignificant = ch
			}
			continue
		}

		if ch == '"' {
			inString = true
			b.WriteByte(ch)
			continue
		}
		if ch == ',' && (lastSignificant == ',' || lastSignificant == '{' || lastSignificant == '[') {
			continue
		}
		b.WriteByte(ch)
		if !isJSONWhitespace(ch) {
			lastSignificant = ch
		}
	}

	return b.String()
}

func dropEllipsisPlaceholders(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	inString := false
	escaped := false

	for i := 0; i < len(value); i++ {
		ch := value[i]
		if inString {
			b.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}

		if ch == '"' {
			inString = true
			b.WriteByte(ch)
			continue
		}
		if strings.HasPrefix(value[i:], "...") {
			i += len("...") - 1
			continue
		}
		b.WriteByte(ch)
	}

	return b.String()
}

func normalizeDuplicateValueSeparators(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	inString := false
	escaped := false

	for i := 0; i < len(value); i++ {
		ch := value[i]
		if inString {
			b.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}

		if ch == '"' {
			inString = true
			b.WriteByte(ch)
			continue
		}
		if ch == ':' && i+1 < len(value) && (value[i+1] == ':' || value[i+1] == '=') {
			b.WriteByte(':')
			i++
			continue
		}
		b.WriteByte(ch)
	}

	return b.String()
}

func quoteExtendedBareStringValues(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	inString := false
	escaped := false

	for i := 0; i < len(value); {
		ch := value[i]
		if inString {
			b.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			i++
			continue
		}

		if ch == '"' {
			inString = true
			b.WriteByte(ch)
			i++
			continue
		}
		if ch != ':' {
			b.WriteByte(ch)
			i++
			continue
		}

		b.WriteByte(ch)
		i++
		for i < len(value) && isJSONWhitespace(value[i]) {
			b.WriteByte(value[i])
			i++
		}
		if i >= len(value) || !isBareStringStart(value[i]) {
			continue
		}

		start := i
		for i < len(value) && !isBareValueDelimiter(value[i]) {
			i++
		}
		raw := strings.TrimRightFunc(value[start:i], unicode.IsSpace)
		trailing := value[start+len(raw) : i]
		if raw != "" && needsExtendedBareStringQuote(raw) {
			escapedValue, _ := json.Marshal(raw)
			b.Write(escapedValue)
		} else {
			b.WriteString(raw)
		}
		b.WriteString(trailing)
	}

	return b.String()
}

func quoteExtendedBareArrayStringValues(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	arrayDepth := 0
	inString := false
	escaped := false
	lastSignificant := byte(0)

	for i := 0; i < len(value); {
		ch := value[i]
		if inString {
			b.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
				lastSignificant = ch
			}
			i++
			continue
		}

		if ch == '"' {
			inString = true
			b.WriteByte(ch)
			i++
			continue
		}
		if ch == '[' {
			arrayDepth++
		} else if ch == ']' && arrayDepth > 0 {
			arrayDepth--
		}
		if arrayDepth == 0 || !isBareStringStart(ch) || (lastSignificant != '[' && lastSignificant != ',') {
			b.WriteByte(ch)
			if !isJSONWhitespace(ch) {
				lastSignificant = ch
			}
			i++
			continue
		}

		start := i
		for i < len(value) && !isBareValueDelimiter(value[i]) {
			i++
		}
		raw := strings.TrimRightFunc(value[start:i], unicode.IsSpace)
		trailing := value[start+len(raw) : i]
		next := nextNonSpaceIndex(value, i)
		if raw != "" && needsExtendedBareArrayStringQuote(raw) && (next < 0 || value[next] != ':') {
			escapedValue, _ := json.Marshal(raw)
			b.Write(escapedValue)
			lastSignificant = '"'
		} else {
			b.WriteString(raw)
			if raw != "" {
				lastSignificant = raw[len(raw)-1]
			}
		}
		b.WriteString(trailing)
	}

	return b.String()
}

func isBareStringStart(ch byte) bool {
	return ch == '_' || ('A' <= ch && ch <= 'Z') || ('a' <= ch && ch <= 'z')
}

func isBareValueDelimiter(ch byte) bool {
	switch ch {
	case ',', '}', ']', '\n', '\r':
		return true
	default:
		return false
	}
}

func needsExtendedBareStringQuote(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool {
		return !(r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r))
	}) >= 0
}

func needsExtendedBareArrayStringQuote(value string) bool {
	if strings.ContainsAny(value, " \t") && !isWhitespaceSeparatedJSONLiteralSequence(value) {
		return true
	}
	return strings.IndexFunc(value, func(r rune) bool {
		switch r {
		case '/', '.', '@', '?', '=', '&', '%', '#', ':':
			return true
		default:
			return false
		}
	}) >= 0
}

func isWhitespaceSeparatedJSONLiteralSequence(value string) bool {
	fields := strings.Fields(value)
	if len(fields) < 2 {
		return false
	}
	for _, field := range fields {
		switch field {
		case "true", "false", "null":
			continue
		default:
			if _, err := strconv.ParseFloat(field, 64); err == nil {
				continue
			}
			return false
		}
	}
	return true
}

func normalizeSingleQuotedStrings(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	inDoubleString := false
	inSingleString := false
	escaped := false

	for i := 0; i < len(value); i++ {
		ch := value[i]
		if inDoubleString {
			b.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inDoubleString = false
			}
			continue
		}
		if inSingleString {
			if escaped {
				if ch == '\'' {
					b.WriteByte('\'')
				} else {
					b.WriteByte('\\')
					b.WriteByte(ch)
				}
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '\'' {
				inSingleString = false
				b.WriteByte('"')
			} else if ch == '"' {
				b.WriteString(`\"`)
			} else if ch < 0x20 {
				writeEscapedControlRune(&b, rune(ch))
			} else {
				b.WriteByte(ch)
			}
			continue
		}

		switch ch {
		case '"':
			inDoubleString = true
			b.WriteByte(ch)
		case '\'':
			inSingleString = true
			b.WriteByte('"')
		default:
			b.WriteByte(ch)
		}
	}

	if inSingleString {
		b.WriteByte('"')
	}
	return b.String()
}

func insertMissingColonBetweenQuotedKeyAndString(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	var stack []byte
	inString := false
	escaped := false
	stringStartedAsPossibleKey := false
	quotedStringMayBeObjectKey := false
	lastSignificant := byte(0)

	for i := 0; i < len(value); i++ {
		ch := value[i]
		if inString {
			b.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
				quotedStringMayBeObjectKey = stringStartedAsPossibleKey
				stringStartedAsPossibleKey = false
				lastSignificant = ch
			}
			continue
		}

		if isJSONWhitespace(ch) {
			start := i
			for i+1 < len(value) && isJSONWhitespace(value[i+1]) {
				i++
			}
			next := nextNonSpaceIndex(value, i+1)
			if quotedStringMayBeObjectKey && next >= 0 && value[next] == '"' {
				b.WriteByte(':')
				quotedStringMayBeObjectKey = false
			}
			b.WriteString(value[start : i+1])
			continue
		}

		switch ch {
		case '"':
			inString = true
			stringStartedAsPossibleKey = len(stack) > 0 && stack[len(stack)-1] == '{' && (lastSignificant == 0 || lastSignificant == '{' || lastSignificant == ',')
		case '{', '[':
			stack = append(stack, ch)
			quotedStringMayBeObjectKey = false
		case '}':
			if len(stack) > 0 && stack[len(stack)-1] == '{' {
				stack = stack[:len(stack)-1]
			}
			quotedStringMayBeObjectKey = false
		case ']':
			if len(stack) > 0 && stack[len(stack)-1] == '[' {
				stack = stack[:len(stack)-1]
			}
			quotedStringMayBeObjectKey = false
		case ':', ',':
			quotedStringMayBeObjectKey = false
		default:
			if !isJSONWhitespace(ch) {
				quotedStringMayBeObjectKey = false
			}
		}
		b.WriteByte(ch)
		lastSignificant = ch
	}

	return b.String()
}

func insertMissingArrayCommas(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	var stack []byte
	inString := false
	escaped := false
	lastSignificant := byte(0)

	for i := 0; i < len(value); i++ {
		ch := value[i]
		if inString {
			b.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
				lastSignificant = ch
			}
			continue
		}

		if isJSONWhitespace(ch) {
			start := i
			for i+1 < len(value) && isJSONWhitespace(value[i+1]) {
				i++
			}
			next := nextNonSpaceIndex(value, i+1)
			if len(stack) > 0 && stack[len(stack)-1] == '[' && isJSONValueEnd(lastSignificant) && next >= 0 && startsArrayValueToken(value[next]) {
				b.WriteByte(',')
			}
			b.WriteString(value[start : i+1])
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, ch)
		case '}':
			if len(stack) > 0 && stack[len(stack)-1] == '{' {
				stack = stack[:len(stack)-1]
			}
		case ']':
			if len(stack) > 0 && stack[len(stack)-1] == '[' {
				stack = stack[:len(stack)-1]
			}
		}
		b.WriteByte(ch)
		lastSignificant = ch
	}

	return b.String()
}

func startsArrayValueToken(ch byte) bool {
	return ch == '"' || ch == '{' || ch == '[' || ch == '-' || ch == '+' || ch == '.' || ('0' <= ch && ch <= '9') || isBareValueStart(ch)
}

func insertMissingObjectCommas(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	objectDepth := 0
	arrayDepth := 0
	inString := false
	escaped := false
	lastSignificant := byte(0)

	for i := 0; i < len(value); i++ {
		ch := value[i]
		if inString {
			b.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
				lastSignificant = ch
			}
			continue
		}

		if isJSONWhitespace(ch) {
			start := i
			for i+1 < len(value) && isJSONWhitespace(value[i+1]) {
				i++
			}
			next := nextNonSpaceIndex(value, i+1)
			if objectDepth > 0 && arrayDepth == 0 && isJSONValueEnd(lastSignificant) && next >= 0 && startsObjectKeyToken(value, next) {
				b.WriteByte(',')
			}
			b.WriteString(value[start : i+1])
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '{':
			objectDepth++
		case '}':
			if objectDepth > 0 {
				objectDepth--
			}
		case '[':
			arrayDepth++
		case ']':
			if arrayDepth > 0 {
				arrayDepth--
			}
		}
		b.WriteByte(ch)
		lastSignificant = ch
	}

	return b.String()
}

func nextNonSpaceIndex(value string, start int) int {
	for i := start; i < len(value); i++ {
		if !isJSONWhitespace(value[i]) {
			return i
		}
	}
	return -1
}

func isJSONWhitespace(ch byte) bool {
	return ch == ' ' || ch == '\n' || ch == '\r' || ch == '\t'
}

func isJSONValueEnd(ch byte) bool {
	return ch == '"' || ch == '}' || ch == ']' || ('0' <= ch && ch <= '9') || ch == 'e' || ch == 'E' || ch == 'l'
}

func startsObjectKeyToken(value string, start int) bool {
	switch value[start] {
	case '"':
		escaped := false
		for i := start + 1; i < len(value); i++ {
			ch := value[i]
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				next := nextNonSpaceIndex(value, i+1)
				return next >= 0 && value[next] == ':'
			}
		}
		return false
	default:
		if !isBareValueStart(value[start]) {
			return false
		}
		i := start
		for i+1 < len(value) && isBareValueChar(value[i+1]) {
			i++
		}
		next := nextNonSpaceIndex(value, i+1)
		return next >= 0 && value[next] == ':'
	}
}

func quoteBareArrayStringValues(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	arrayDepth := 0
	inString := false
	escaped := false

	for i := 0; i < len(value); i++ {
		ch := value[i]
		if inString {
			b.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
			b.WriteByte(ch)
		case '[':
			arrayDepth++
			b.WriteByte(ch)
		case ']':
			if arrayDepth > 0 {
				arrayDepth--
			}
			b.WriteByte(ch)
		default:
			if arrayDepth > 0 && isBareValueStart(ch) {
				start := i
				for i+1 < len(value) && isBareValueChar(value[i+1]) {
					i++
				}
				token := value[start : i+1]
				if shouldQuoteBareJSONToken(token) {
					b.WriteByte('"')
					b.WriteString(token)
					b.WriteByte('"')
				} else {
					b.WriteString(token)
				}
				continue
			}
			b.WriteByte(ch)
		}
	}

	return b.String()
}

func isBareValueStart(ch byte) bool {
	return ch == '_' || ('A' <= ch && ch <= 'Z') || ('a' <= ch && ch <= 'z')
}

func isBareValueChar(ch byte) bool {
	return isBareValueStart(ch) || ('0' <= ch && ch <= '9') || ch == '-'
}

func shouldQuoteBareJSONToken(token string) bool {
	switch token {
	case "true", "false", "null":
		return false
	default:
		return true
	}
}

func extractJSONObjectFromSurroundingText(value string) string {
	start := strings.IndexByte(value, '{')
	if start < 0 {
		return value
	}

	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(value); i++ {
		ch := value[i]
		if inString {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return value[start : i+1]
			}
		}
	}

	return value
}

func normalizeAlternateSeparators(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	inString := false
	escaped := false

	for i := 0; i < len(value); i++ {
		ch := value[i]
		if inString {
			b.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
			b.WriteByte(ch)
		case ';', '|':
			b.WriteByte(',')
		default:
			b.WriteByte(ch)
		}
	}

	return b.String()
}

func normalizeTupleLikeArrays(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	inString := false
	escaped := false

	for i := 0; i < len(value); i++ {
		ch := value[i]
		if inString {
			b.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
			b.WriteByte(ch)
		case '(':
			b.WriteByte('[')
		case ')':
			b.WriteByte(']')
		default:
			b.WriteByte(ch)
		}
	}

	return b.String()
}

func normalizeNonstandardNumberLiterals(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	inString := false
	escaped := false

	for i := 0; i < len(value); {
		ch := value[i]
		if inString {
			b.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			i++
			continue
		}

		if ch == '"' {
			inString = true
			b.WriteByte(ch)
			i++
			continue
		}

		if ch == '+' && i+1 < len(value) && isDigit(value[i+1]) && hasNumberPrefixBoundary(value, i) {
			i++
			continue
		}
		if ch == '+' && i+1 < len(value) && value[i+1] == '.' && hasNumberPrefixBoundary(value, i) {
			b.WriteByte('0')
			i++
			continue
		}
		if ch == '.' && i+1 < len(value) && isDigit(value[i+1]) && hasNumberPrefixBoundary(value, i) {
			b.WriteString("0.")
			i++
			continue
		}
		if ch == '-' && i+2 < len(value) && value[i+1] == '.' && isDigit(value[i+2]) && hasNumberPrefixBoundary(value, i) {
			b.WriteString("-0.")
			i += 2
			continue
		}
		b.WriteByte(ch)
		i++
	}

	return b.String()
}

func hasNumberPrefixBoundary(value string, offset int) bool {
	if offset == 0 {
		return true
	}
	return isJSONTokenBoundary(value[offset-1])
}

func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

func normalizePythonBooleanLiterals(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	inString := false
	escaped := false

	for i := 0; i < len(value); {
		ch := value[i]
		if inString {
			b.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			i++
			continue
		}

		if ch == '"' {
			inString = true
			b.WriteByte(ch)
			i++
			continue
		}
		switch {
		case hasJSONToken(value, i, "True"):
			b.WriteString("true")
			i += len("True")
			continue
		case hasJSONToken(value, i, "False"):
			b.WriteString("false")
			i += len("False")
			continue
		case hasJSONToken(value, i, "None"):
			b.WriteString(`"None"`)
			i += len("None")
			continue
		default:
			b.WriteByte(ch)
			i++
		}
	}

	return b.String()
}

func hasJSONToken(value string, offset int, token string) bool {
	if !strings.HasPrefix(value[offset:], token) {
		return false
	}
	before := byte(0)
	if offset > 0 {
		before = value[offset-1]
	}
	afterOffset := offset + len(token)
	after := byte(0)
	if afterOffset < len(value) {
		after = value[afterOffset]
	}
	return isJSONTokenBoundary(before) && isJSONTokenBoundary(after)
}

func isJSONTokenBoundary(ch byte) bool {
	switch ch {
	case 0, ' ', '\n', '\r', '\t', ':', ',', '[', ']', '{', '}':
		return true
	default:
		return false
	}
}

func stripJSONComments(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	inString := false
	escaped := false

	for i := 0; i < len(value); i++ {
		ch := value[i]
		if inString {
			b.WriteByte(ch)
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

		if ch == '"' {
			inString = true
			b.WriteByte(ch)
			continue
		}
		if ch == '#' {
			for i < len(value) && value[i] != '\n' && value[i] != '\r' {
				i++
			}
			if i < len(value) {
				b.WriteByte(value[i])
			}
			continue
		}
		if ch == '/' && i+1 < len(value) {
			switch value[i+1] {
			case '/':
				i += 2
				for i < len(value) && value[i] != '\n' && value[i] != '\r' {
					i++
				}
				if i < len(value) {
					b.WriteByte(value[i])
				}
				continue
			case '*':
				i += 2
				for i+1 < len(value) && !(value[i] == '*' && value[i+1] == '/') {
					i++
				}
				if i+1 < len(value) {
					i++
				}
				continue
			}
		}
		b.WriteByte(ch)
	}

	return b.String()
}

func escapeStringControlCharacters(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	inString := false
	escaped := false

	for _, r := range value {
		if inString {
			if escaped {
				b.WriteRune(r)
				escaped = false
				continue
			}
			switch r {
			case '\\':
				b.WriteRune(r)
				escaped = true
				continue
			case '"':
				b.WriteRune(r)
				inString = false
				continue
			case '\n':
				b.WriteString(`\n`)
				continue
			case '\r':
				b.WriteString(`\r`)
				continue
			case '\t':
				b.WriteString(`\t`)
				continue
			}
			if r < 0x20 {
				writeEscapedControlRune(&b, r)
				continue
			}
		} else if r == '"' {
			inString = true
		}
		b.WriteRune(r)
	}

	return b.String()
}

func writeEscapedControlRune(b *strings.Builder, r rune) {
	switch r {
	case '\b':
		b.WriteString(`\b`)
	case '\f':
		b.WriteString(`\f`)
	default:
		fmt.Fprintf(b, `\u%04x`, r)
	}
}

func quoteUnquotedStringValues(value string) string {
	return unquotedStringValuePattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := unquotedStringValuePattern.FindStringSubmatch(match)
		if len(parts) != 4 {
			return match
		}
		switch parts[2] {
		case "true", "false", "null":
			return match
		default:
			return parts[1] + `"` + parts[2] + `"` + parts[3]
		}
	})
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

	if inString {
		insertAt := len(value)
		pending := append([]byte(nil), stack...)
		for len(pending) > 0 && insertAt > 0 && value[insertAt-1] == pending[len(pending)-1] {
			insertAt--
			pending = pending[:len(pending)-1]
		}
		var b strings.Builder
		b.Grow(len(value) + 1 + len(pending))
		b.WriteString(value[:insertAt])
		b.WriteByte('"')
		b.WriteString(value[insertAt:])
		for i := len(pending) - 1; i >= 0; i-- {
			b.WriteByte(pending[i])
		}
		return b.String()
	}
	if len(stack) == 0 {
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
	if isNilLLMStream(stream) {
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
	if isNilLLMStream(stream) {
		return nil, fmt.Errorf("llm stream is nil")
	}
	return &TextStream{stream: stream}, nil
}

func (s *TextStream) Next() (string, error) {
	if s == nil || isNilLLMStream(s.stream) {
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
	extra := toolCall.Extra
	if extra == nil {
		extra = make(map[string]any)
	}
	fncCall := FunctionCall{
		ID:        toolCall.ID,
		CallID:    toolCall.CallID,
		Name:      toolCall.Name,
		Arguments: args,
		Extra:     extra,
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
	if ToolDuplicateModeFor(tool) == ToolDuplicateModeConfirm {
		delete(parsedArgs, ConfirmDuplicateParam)
	}
	encodedArgs, err := json.Marshal(parsedArgs)
	if err != nil {
		return MakeToolOutput(fncCall, nil, err)
	}
	args = string(encodedArgs)
	fncCall.Arguments = args
	toolCall.Arguments = args

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
