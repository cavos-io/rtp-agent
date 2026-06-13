package llm

import (
	"context"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestStripThinkingTokensTracksHiddenChunks(t *testing.T) {
	thinking := false

	if got, ok := StripThinkingTokens("hello", &thinking); !ok || got != "hello" || thinking {
		t.Fatalf("plain content = (%q, %v, thinking=%v), want visible hello", got, ok, thinking)
	}

	if got, ok := StripThinkingTokens("<think>", &thinking); !ok || got != "" || !thinking {
		t.Fatalf("think start = (%q, %v, thinking=%v), want visible empty and thinking", got, ok, thinking)
	}

	if got, ok := StripThinkingTokens("hidden reasoning", &thinking); ok || got != "" || !thinking {
		t.Fatalf("hidden chunk = (%q, %v, thinking=%v), want suppressed and thinking", got, ok, thinking)
	}

	if got, ok := StripThinkingTokens("</think>visible", &thinking); !ok || got != "visible" || thinking {
		t.Fatalf("think end = (%q, %v, thinking=%v), want visible content and not thinking", got, ok, thinking)
	}
}

func TestSerializeImageRejectsUnsupportedMIMETypeWithReferenceError(t *testing.T) {
	_, err := SerializeImage(&ImageContent{
		Image: "data:image/bmp;base64,AA==",
	})
	if err == nil {
		t.Fatal("SerializeImage() error = nil, want unsupported mime_type error")
	}

	want := "Unsupported mime_type image/bmp. Must be jpeg, png, webp, or gif"
	if err.Error() != want {
		t.Fatalf("SerializeImage() error = %q, want %q", err, want)
	}
}

func TestSerializeImageRejectsUnsupportedImageTypeWithReferenceError(t *testing.T) {
	_, err := SerializeImage(&ImageContent{Image: 42})
	if err == nil {
		t.Fatal("SerializeImage() error = nil, want unsupported image type error")
	}

	want := "Unsupported image type"
	if err.Error() != want {
		t.Fatalf("SerializeImage() error = %q, want %q", err, want)
	}
}

func TestParseFunctionArgumentsParsesJSONObject(t *testing.T) {
	args, err := ParseFunctionArguments(`{"city":"Paris","limit":3}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" || args["limit"] != float64(3) {
		t.Fatalf("args = %#v, want parsed city and limit", args)
	}
}

func TestParseFunctionArgumentsUnwrapsNestedJSONString(t *testing.T) {
	args, err := ParseFunctionArguments(`"{\"city\":\"Paris\"}"`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" {
		t.Fatalf("args = %#v, want nested JSON object", args)
	}
}

func TestParseFunctionArgumentsRejectsNestedNonJSONStringWithReferenceError(t *testing.T) {
	_, err := ParseFunctionArguments(`"not json object"`)
	if err == nil {
		t.Fatal("ParseFunctionArguments(nested string) error = nil, want error")
	}

	want := "function arguments decoded to a non-JSON string: not json object"
	if err.Error() != want {
		t.Fatalf("ParseFunctionArguments(nested string) error = %q, want %q", err.Error(), want)
	}
}

func TestParseFunctionArgumentsRejectsNumericNonObjectWithReferenceError(t *testing.T) {
	_, err := ParseFunctionArguments(`3`)
	if err == nil {
		t.Fatal("ParseFunctionArguments(number) error = nil, want error")
	}

	want := "expected dict from function arguments, got int: 3"
	if err.Error() != want {
		t.Fatalf("ParseFunctionArguments(number) error = %q, want %q", err.Error(), want)
	}
}

func TestParseFunctionArgumentsRepairsLeakedTemplateTokens(t *testing.T) {
	args, err := ParseFunctionArguments(`{"city":"Paris"}<|im_end|>`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" {
		t.Fatalf("args = %#v, want repaired city", args)
	}
}

func TestParseFunctionArgumentsDropsListItemsEmptiedByTemplateRepair(t *testing.T) {
	args, err := ParseFunctionArguments(`{"tags":["<|im_start|>","urgent"]}<|im_end|>`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	tags, ok := args["tags"].([]any)
	if !ok {
		t.Fatalf("tags = %#v, want []any", args["tags"])
	}
	if len(tags) != 1 || tags[0] != "urgent" {
		t.Fatalf("tags = %#v, want only urgent after dropping empty repaired token", tags)
	}
}

func TestParseFunctionArgumentsRepairsTrailingCommas(t *testing.T) {
	args, err := ParseFunctionArguments(`{"city":"Paris","limit":3,}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" || args["limit"] != float64(3) {
		t.Fatalf("args = %#v, want repaired city and limit", args)
	}
}

func TestParseFunctionArgumentsRepairsDuplicateCommas(t *testing.T) {
	args, err := ParseFunctionArguments(`{"city":"Paris",, "limit":3, "items":[urgent,,home], "note":"keep,,literal"}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" || args["limit"] != float64(3) || args["note"] != "keep,,literal" {
		t.Fatalf("args = %#v, want duplicate commas removed outside strings", args)
	}
	items, ok := args["items"].([]any)
	if !ok || len(items) != 2 || items[0] != "urgent" || items[1] != "home" {
		t.Fatalf("items = %#v, want duplicate comma slot removed", args["items"])
	}
}

func TestParseFunctionArgumentsRepairsLeadingCommas(t *testing.T) {
	args, err := ParseFunctionArguments(`{,"city":"Paris","items":[,urgent,home],"note":"keep, literal"}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" || args["note"] != "keep, literal" {
		t.Fatalf("args = %#v, want leading comma slots removed outside strings", args)
	}
	items, ok := args["items"].([]any)
	if !ok || len(items) != 2 || items[0] != "urgent" || items[1] != "home" {
		t.Fatalf("items = %#v, want leading array comma slot removed", args["items"])
	}
}

func TestParseFunctionArgumentsDropsEllipsisPlaceholders(t *testing.T) {
	args, err := ParseFunctionArguments(`{"city":"Paris","limit":3,...}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" || args["limit"] != float64(3) {
		t.Fatalf("args = %#v, want ellipsis placeholder dropped", args)
	}
}

func TestParseFunctionArgumentsRepairsMissingClosingDelimiter(t *testing.T) {
	args, err := ParseFunctionArguments(`{"city":"Paris","tags":["metro","food"]`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" {
		t.Fatalf("city = %#v, want Paris", args["city"])
	}
	tags, ok := args["tags"].([]any)
	if !ok || len(tags) != 2 || tags[0] != "metro" || tags[1] != "food" {
		t.Fatalf("tags = %#v, want metro and food", args["tags"])
	}
}

func TestParseFunctionArgumentsRepairsUnquotedObjectKeys(t *testing.T) {
	args, err := ParseFunctionArguments(`{city:"Paris",limit:3}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" || args["limit"] != float64(3) {
		t.Fatalf("args = %#v, want repaired city and limit", args)
	}
}

func TestParseFunctionArgumentsRepairsUnquotedStringValues(t *testing.T) {
	args, err := ParseFunctionArguments(`{"city":Paris,"country":FR,"limit":3}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" || args["country"] != "FR" || args["limit"] != float64(3) {
		t.Fatalf("args = %#v, want repaired string values and limit", args)
	}
}

func TestParseFunctionArgumentsRepairsBareURLAndIdentifierValues(t *testing.T) {
	args, err := ParseFunctionArguments(`{"url":https://example.com/a-b?q=1,"email":user@example.com,"version":v1.2.3}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["url"] != "https://example.com/a-b?q=1" || args["email"] != "user@example.com" || args["version"] != "v1.2.3" {
		t.Fatalf("args = %#v, want repaired bare URL and identifier string values", args)
	}
}

func TestParseFunctionArgumentsRepairsSingleQuotedValues(t *testing.T) {
	args, err := ParseFunctionArguments(`{'city':'Paris','country':'FR'}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" || args["country"] != "FR" {
		t.Fatalf("args = %#v, want repaired city and country", args)
	}
}

func TestParseFunctionArgumentsRepairsEscapedSingleQuotedValues(t *testing.T) {
	args, err := ParseFunctionArguments(`{'note':'Bob\'s place','items':['owner\'s','guest']}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["note"] != "Bob's place" {
		t.Fatalf("note = %#v, want escaped apostrophe repaired", args["note"])
	}
	items, ok := args["items"].([]any)
	if !ok || len(items) != 2 || items[0] != "owner's" || items[1] != "guest" {
		t.Fatalf("items = %#v, want escaped apostrophe array repaired", args["items"])
	}
}

func TestParseFunctionArgumentsRepairsDoubleQuotesInsideSingleQuotedValues(t *testing.T) {
	args, err := ParseFunctionArguments(`{'note':'say "hi"','items':['owner says "go"']}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["note"] != `say "hi"` {
		t.Fatalf("note = %#v, want embedded double quotes preserved", args["note"])
	}
	items, ok := args["items"].([]any)
	if !ok || len(items) != 1 || items[0] != `owner says "go"` {
		t.Fatalf("items = %#v, want embedded double quotes preserved", args["items"])
	}
}

func TestParseFunctionArgumentsRepairsRawControlCharactersInsideSingleQuotedValues(t *testing.T) {
	args, err := ParseFunctionArguments("{'line':'hello\nworld','tab':'hello\tworld'}")
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["line"] != "hello\nworld" {
		t.Fatalf("line = %#v, want raw newline preserved after repair", args["line"])
	}
	if args["tab"] != "hello\tworld" {
		t.Fatalf("tab = %#v, want raw tab preserved after repair", args["tab"])
	}
}

func TestParseFunctionArgumentsRepairsRawNewlineStringValues(t *testing.T) {
	args, err := ParseFunctionArguments("{\"message\":\"hello\nworld\"}")
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["message"] != "hello\nworld" {
		t.Fatalf("message = %#v, want raw newline preserved after repair", args["message"])
	}
}

func TestParseFunctionArgumentsRepairsRawControlStringValues(t *testing.T) {
	args, err := ParseFunctionArguments("{\"message\":\"hello\bworld\fdone\"}")
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["message"] != "hello\bworld\fdone" {
		t.Fatalf("message = %#v, want raw control characters preserved after repair", args["message"])
	}
}

func TestParseFunctionArgumentsRepairsUnterminatedDoubleQuotedValue(t *testing.T) {
	args, err := ParseFunctionArguments(`{"city":"Paris}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" {
		t.Fatalf("city = %#v, want unterminated string value repaired", args["city"])
	}
}

func TestParseFunctionArgumentsRepairsJSONComments(t *testing.T) {
	args, err := ParseFunctionArguments("{\"city\":\"Paris\", // destination\n \"limit\":3, /* priority */ \"unit\":\"km\"}")
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" || args["limit"] != float64(3) || args["unit"] != "km" {
		t.Fatalf("args = %#v, want repaired object with comments removed", args)
	}
}

func TestParseFunctionArgumentsRepairsHashComments(t *testing.T) {
	args, err := ParseFunctionArguments("{\"city\":\"Paris\", # destination\n \"limit\":3, \"note\":\"keep # literal\"}")
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" || args["limit"] != float64(3) || args["note"] != "keep # literal" {
		t.Fatalf("args = %#v, want repaired object with hash comments removed outside strings", args)
	}
}

func TestParseFunctionArgumentsRepairsPythonBooleanLiterals(t *testing.T) {
	args, err := ParseFunctionArguments(`{"enabled": True, "disabled": False, "flags":[True,False]}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["enabled"] != true || args["disabled"] != false {
		t.Fatalf("args = %#v, want Python boolean literals repaired to JSON booleans", args)
	}
	flags, ok := args["flags"].([]any)
	if !ok || len(flags) != 2 || flags[0] != true || flags[1] != false {
		t.Fatalf("flags = %#v, want repaired boolean array", args["flags"])
	}
}

func TestParseFunctionArgumentsRepairsPythonNoneLiteralAsString(t *testing.T) {
	args, err := ParseFunctionArguments(`{"value": None, "items":[None]}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["value"] != "None" {
		t.Fatalf("value = %#v, want repaired None string", args["value"])
	}
	items, ok := args["items"].([]any)
	if !ok || len(items) != 1 || items[0] != "None" {
		t.Fatalf("items = %#v, want repaired None string array", args["items"])
	}
}

func TestParseFunctionArgumentsRepairsNonstandardNumberLiterals(t *testing.T) {
	args, err := ParseFunctionArguments(`{"limit": +3, "ratio": .5, "negative": -.25, "positive": +.75}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["limit"] != float64(3) || args["ratio"] != 0.5 || args["negative"] != -0.25 || args["positive"] != 0.75 {
		t.Fatalf("args = %#v, want repaired nonstandard numeric literals", args)
	}
}

func TestParseFunctionArgumentsRepairsTupleLikeArrays(t *testing.T) {
	args, err := ParseFunctionArguments(`{"numbers": (1,2), "labels": ("fast","safe")}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	numbers, ok := args["numbers"].([]any)
	if !ok || len(numbers) != 2 || numbers[0] != float64(1) || numbers[1] != float64(2) {
		t.Fatalf("numbers = %#v, want repaired numeric tuple", args["numbers"])
	}
	labels, ok := args["labels"].([]any)
	if !ok || len(labels) != 2 || labels[0] != "fast" || labels[1] != "safe" {
		t.Fatalf("labels = %#v, want repaired string tuple", args["labels"])
	}
}

func TestParseFunctionArgumentsRepairsSemicolonSeparators(t *testing.T) {
	args, err := ParseFunctionArguments(`{"city":"Paris"; "limit":3; "note":"keep;semicolon"}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" || args["limit"] != float64(3) || args["note"] != "keep;semicolon" {
		t.Fatalf("args = %#v, want semicolon separators repaired outside strings", args)
	}
}

func TestParseFunctionArgumentsRepairsPipeSeparators(t *testing.T) {
	args, err := ParseFunctionArguments(`{"city":"Paris" | "limit":3 | "note":"keep | literal"}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" || args["limit"] != float64(3) || args["note"] != "keep | literal" {
		t.Fatalf("args = %#v, want pipe separators repaired outside strings", args)
	}
}

func TestParseFunctionArgumentsExtractsObjectFromSurroundingText(t *testing.T) {
	args, err := ParseFunctionArguments(`call tool with {"city":"Paris","note":"keep } literal"} thanks`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" || args["note"] != "keep } literal" {
		t.Fatalf("args = %#v, want JSON object extracted from surrounding text", args)
	}
}

func TestParseFunctionArgumentsRepairsBareArrayStringValues(t *testing.T) {
	args, err := ParseFunctionArguments(`{"items":[urgent,home], "nums":[1,+2], "flags":[true,false]}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	items, ok := args["items"].([]any)
	if !ok || len(items) != 2 || items[0] != "urgent" || items[1] != "home" {
		t.Fatalf("items = %#v, want repaired string array", args["items"])
	}
	nums, ok := args["nums"].([]any)
	if !ok || len(nums) != 2 || nums[0] != float64(1) || nums[1] != float64(2) {
		t.Fatalf("nums = %#v, want numeric array preserved", args["nums"])
	}
	flags, ok := args["flags"].([]any)
	if !ok || len(flags) != 2 || flags[0] != true || flags[1] != false {
		t.Fatalf("flags = %#v, want boolean array preserved", args["flags"])
	}
}

func TestParseFunctionArgumentsRepairsBareURLArrayValues(t *testing.T) {
	args, err := ParseFunctionArguments(`{"urls":[https://example.com/a-b?q=1,user@example.com,v1.2.3], "mixed":["quoted",https://example.com,true,3]}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	urls, ok := args["urls"].([]any)
	if !ok || len(urls) != 3 || urls[0] != "https://example.com/a-b?q=1" || urls[1] != "user@example.com" || urls[2] != "v1.2.3" {
		t.Fatalf("urls = %#v, want repaired URL-like string array", args["urls"])
	}
	mixed, ok := args["mixed"].([]any)
	if !ok || len(mixed) != 4 || mixed[0] != "quoted" || mixed[1] != "https://example.com" || mixed[2] != true || mixed[3] != float64(3) {
		t.Fatalf("mixed = %#v, want repaired URL-like value with booleans and numbers preserved", args["mixed"])
	}
}

func TestParseFunctionArgumentsRepairsBareMultiWordArrayValues(t *testing.T) {
	args, err := ParseFunctionArguments(`{"cities":[New York,San Francisco], "labels":[high priority,low risk,true,3]}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	cities, ok := args["cities"].([]any)
	if !ok || len(cities) != 2 || cities[0] != "New York" || cities[1] != "San Francisco" {
		t.Fatalf("cities = %#v, want repaired multi-word string array", args["cities"])
	}
	labels, ok := args["labels"].([]any)
	if !ok || len(labels) != 4 || labels[0] != "high priority" || labels[1] != "low risk" || labels[2] != true || labels[3] != float64(3) {
		t.Fatalf("labels = %#v, want repaired multi-word values with booleans and numbers preserved", args["labels"])
	}
}

func TestParseFunctionArgumentsRepairsMissingObjectCommas(t *testing.T) {
	args, err := ParseFunctionArguments(`{"city":"Paris" "limit":3, "nested":{"unit":"celsius" "enabled":true}}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" || args["limit"] != float64(3) {
		t.Fatalf("args = %#v, want repaired top-level object members", args)
	}
	nested, ok := args["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested = %#v, want object", args["nested"])
	}
	if nested["unit"] != "celsius" || nested["enabled"] != true {
		t.Fatalf("nested = %#v, want repaired nested object members", nested)
	}
}

func TestParseFunctionArgumentsRepairsMissingArrayCommas(t *testing.T) {
	args, err := ParseFunctionArguments(`{"items":["urgent" "home"], "nums":[1 2 3], "flags":[true false]}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	items, ok := args["items"].([]any)
	if !ok || len(items) != 2 || items[0] != "urgent" || items[1] != "home" {
		t.Fatalf("items = %#v, want string array commas repaired", args["items"])
	}
	nums, ok := args["nums"].([]any)
	if !ok || len(nums) != 3 || nums[0] != float64(1) || nums[1] != float64(2) || nums[2] != float64(3) {
		t.Fatalf("nums = %#v, want numeric array commas repaired", args["nums"])
	}
	flags, ok := args["flags"].([]any)
	if !ok || len(flags) != 2 || flags[0] != true || flags[1] != false {
		t.Fatalf("flags = %#v, want boolean array commas repaired", args["flags"])
	}
}

func TestParseFunctionArgumentsRepairsMissingColonBetweenQuotedKeyAndString(t *testing.T) {
	args, err := ParseFunctionArguments(`{"city" "Paris", "nested":{"unit" "celsius"}}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" {
		t.Fatalf("city = %#v, want repaired quoted string value", args["city"])
	}
	nested, ok := args["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested = %#v, want object", args["nested"])
	}
	if nested["unit"] != "celsius" {
		t.Fatalf("nested unit = %#v, want repaired quoted string value", nested["unit"])
	}
}

func TestParseFunctionArgumentsRepairsDuplicateValueSeparators(t *testing.T) {
	args, err := ParseFunctionArguments(`{"city"::"Paris", "limit":=3, "note":"keep :: literal"}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" || args["limit"] != float64(3) || args["note"] != "keep :: literal" {
		t.Fatalf("args = %#v, want duplicate separators repaired outside strings", args)
	}
}

func TestParseFunctionArgumentsTreatsNullAsEmptyObject(t *testing.T) {
	args, err := ParseFunctionArguments(`null`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if len(args) != 0 {
		t.Fatalf("args = %#v, want empty map", args)
	}
}

func TestParseFunctionArgumentsRejectsNonObject(t *testing.T) {
	if _, err := ParseFunctionArguments(`["Paris"]`); err == nil {
		t.Fatal("ParseFunctionArguments(array) error = nil, want error")
	}
	if _, err := ParseFunctionArguments(`"not json object"`); err == nil {
		t.Fatal("ParseFunctionArguments(string) error = nil, want error")
	}
}

func TestParseFunctionArgumentsRejectsNonObjectWithReferenceError(t *testing.T) {
	_, err := ParseFunctionArguments(`["Paris"]`)
	if err == nil {
		t.Fatal("ParseFunctionArguments(array) error = nil, want error")
	}

	want := `expected dict from function arguments, got list: ["Paris"]`
	if err.Error() != want {
		t.Fatalf("ParseFunctionArguments(array) error = %q, want %q", err.Error(), want)
	}
}

func TestParseFunctionArgumentsReportsRawPrefixWhenRepairIsEmpty(t *testing.T) {
	const raw = `<|im_end|>`

	_, err := ParseFunctionArguments(raw)
	if err == nil {
		t.Fatal("ParseFunctionArguments(template token) error = nil, want error")
	}

	if !strings.HasPrefix(err.Error(), "could not parse function arguments as JSON: ") {
		t.Fatalf("ParseFunctionArguments(template token) error = %q, want could-not-parse category", err.Error())
	}
	if !strings.HasSuffix(err.Error(), ": "+raw) {
		t.Fatalf("ParseFunctionArguments(template token) error = %q, want raw argument prefix suffix", err.Error())
	}
}

func TestMakeFunctionCallOutputUsesToolErrorMessage(t *testing.T) {
	call := FunctionCall{CallID: "call_lookup", Name: "lookup", Arguments: "{}"}

	result := MakeFunctionCallOutput(call, nil, NewToolError("visible failure"))

	if result.FncCall.CallID != call.CallID || result.FncCall.Name != call.Name || result.FncCall.Arguments != call.Arguments {
		t.Fatalf("FncCall = %#v, want original call", result.FncCall)
	}
	if result.FncCallOut == nil {
		t.Fatal("FncCallOut = nil, want visible tool error output")
	}
	if !result.FncCallOut.IsError || result.FncCallOut.Output != "visible failure" {
		t.Fatalf("FncCallOut = %#v, want visible error output", result.FncCallOut)
	}
	if result.RawError == nil {
		t.Fatal("RawError = nil, want original error")
	}
}

func TestMakeToolOutputReturnsVisibleOutputAndRawValues(t *testing.T) {
	call := FunctionCall{CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`}

	result := MakeToolOutput(call, "Paris", nil)

	if result.FncCall.CallID != call.CallID || result.FncCall.Name != call.Name || result.FncCall.Arguments != call.Arguments {
		t.Fatalf("FncCall = %#v, want original call", result.FncCall)
	}
	if result.FncCallOut == nil {
		t.Fatal("FncCallOut = nil, want successful output")
	}
	if result.FncCallOut.IsError || result.FncCallOut.Output != "Paris" {
		t.Fatalf("FncCallOut = %#v, want visible Paris output", result.FncCallOut)
	}
	if result.RawOutput != "Paris" {
		t.Fatalf("RawOutput = %#v, want original raw output", result.RawOutput)
	}
	if result.RawError != nil {
		t.Fatalf("RawError = %v, want nil", result.RawError)
	}
}

func TestMakeFunctionCallOutputSuppressesStopResponse(t *testing.T) {
	call := FunctionCall{CallID: "call_lookup", Name: "lookup", Arguments: "{}"}

	result := MakeFunctionCallOutput(call, nil, StopResponse{})

	if result.FncCallOut != nil {
		t.Fatalf("FncCallOut = %#v, want nil for StopResponse", result.FncCallOut)
	}
	if result.RawError == nil {
		t.Fatal("RawError = nil, want StopResponse")
	}
}

func TestMakeFunctionCallOutputMasksInternalErrors(t *testing.T) {
	call := FunctionCall{CallID: "call_lookup", Name: "lookup", Arguments: "{}"}

	result := MakeFunctionCallOutput(call, nil, errors.New("database password leaked"))

	if result.FncCallOut == nil {
		t.Fatal("FncCallOut = nil, want masked internal error output")
	}
	if !result.FncCallOut.IsError || result.FncCallOut.Output != "An internal error occurred" {
		t.Fatalf("FncCallOut = %#v, want masked internal error", result.FncCallOut)
	}
	if result.RawError == nil {
		t.Fatal("RawError = nil, want original error")
	}
}

func TestMakeFunctionCallOutputStringifiesValidOutputs(t *testing.T) {
	call := FunctionCall{CallID: "call_lookup", Name: "lookup", Arguments: "{}"}

	tests := []struct {
		name   string
		output any
		want   string
	}{
		{name: "integer", output: 7, want: "7"},
		{name: "positive infinity", output: math.Inf(1), want: "inf"},
		{name: "negative infinity", output: math.Inf(-1), want: "-inf"},
		{name: "exponent float", output: 1e20, want: "1e+20"},
		{name: "true", output: true, want: "True"},
		{name: "complex", output: complex(1, 2), want: "(1+2j)"},
		{name: "complex positive infinity", output: complex(math.Inf(1), 2), want: "(inf+2j)"},
		{name: "complex imaginary infinity", output: complex(1, math.Inf(1)), want: "(1+infj)"},
		{name: "complex nan", output: complex(math.NaN(), 2), want: "(nan+2j)"},
		{name: "complex negative zero imaginary", output: complex(1, math.Copysign(0, -1)), want: "(1-0j)"},
		{name: "complex negative zero real", output: complex(math.Copysign(0, -1), 2), want: "(-0+2j)"},
		{name: "list", output: []any{1, "x", true}, want: "[1, 'x', True]"},
		{name: "list floats", output: []any{0.0, math.Copysign(0, -1), 1.0, 1.5}, want: "[0.0, -0.0, 1.0, 1.5]"},
		{name: "list exponent floats", output: []any{1e20, 1e-7, 1e-5}, want: "[1e+20, 1e-07, 1e-05]"},
		{name: "list string newline", output: []any{"line\nnext"}, want: "['line\\nnext']"},
		{name: "list string apostrophe", output: []any{"can't"}, want: `["can't"]`},
		{name: "list string nul", output: []any{"\x00"}, want: `['\x00']`},
		{name: "list string backspace", output: []any{"\b"}, want: `['\x08']`},
		{name: "list string escape", output: []any{"\x1b"}, want: `['\x1b']`},
		{name: "list string next line", output: []any{"\u0085"}, want: `['\x85']`},
		{name: "list string line separator", output: []any{"\u2028"}, want: `['\u2028']`},
		{name: "list string non-ascii printable", output: []any{"é"}, want: "['é']"},
		{name: "tuple", output: [3]any{1, "x", true}, want: "(1, 'x', True)"},
		{name: "singleton tuple", output: [1]any{1}, want: "(1,)"},
		{name: "dict", output: map[string]any{"ok": true}, want: "{'ok': True}"},
		{name: "dict float", output: map[string]any{"score": 1.0}, want: "{'score': 1.0}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MakeFunctionCallOutput(call, tt.output, nil)

			if result.FncCallOut == nil {
				t.Fatal("FncCallOut = nil, want successful output")
			}
			if result.FncCallOut.IsError || result.FncCallOut.Output != tt.want {
				t.Fatalf("FncCallOut = %#v, want output %q", result.FncCallOut, tt.want)
			}
			if !functionOutputTestEqual(result.RawOutput, tt.output) {
				t.Fatalf("RawOutput = %#v, want original output %#v", result.RawOutput, tt.output)
			}
		})
	}
}

func functionOutputTestEqual(got, want any) bool {
	switch wantValue := want.(type) {
	case complex128:
		gotValue, ok := got.(complex128)
		if !ok {
			return false
		}
		return floatTestEqual(real(gotValue), real(wantValue)) && floatTestEqual(imag(gotValue), imag(wantValue))
	default:
		return reflect.DeepEqual(got, want)
	}
}

func floatTestEqual(got, want float64) bool {
	if math.IsNaN(want) {
		return math.IsNaN(got)
	}
	return got == want
}

func TestMakeFunctionCallOutputUsesEmptyStringForFalsyOutputs(t *testing.T) {
	call := FunctionCall{CallID: "call_lookup", Name: "lookup", Arguments: "{}"}

	tests := []struct {
		name   string
		output any
	}{
		{name: "false", output: false},
		{name: "zero int", output: 0},
		{name: "zero float", output: 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MakeFunctionCallOutput(call, tt.output, nil)

			if result.FncCallOut == nil {
				t.Fatal("FncCallOut = nil, want successful output")
			}
			if result.FncCallOut.IsError || result.FncCallOut.Output != "" {
				t.Fatalf("FncCallOut = %#v, want empty successful output", result.FncCallOut)
			}
			if result.RawOutput != tt.output {
				t.Fatalf("RawOutput = %#v, want original output %#v", result.RawOutput, tt.output)
			}
		})
	}
}

func TestMakeFunctionCallOutputTimestampsCreatedOutputs(t *testing.T) {
	call := FunctionCall{CallID: "call_lookup", Name: "lookup", Arguments: "{}"}
	tests := []struct {
		name      string
		output    any
		exception error
	}{
		{name: "success", output: "Paris"},
		{name: "tool error", exception: NewToolError("visible failure")},
		{name: "internal error", exception: errors.New("database failure")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MakeFunctionCallOutput(call, tt.output, tt.exception)

			if result.FncCallOut == nil {
				t.Fatal("FncCallOut = nil, want created output")
			}
			if result.FncCallOut.CreatedAt.IsZero() {
				t.Fatal("CreatedAt is zero, want generated timestamp")
			}
		})
	}
}

func TestMakeFunctionCallOutputDropsInvalidStructuredOutputs(t *testing.T) {
	call := FunctionCall{CallID: "call_lookup", Name: "lookup", Arguments: "{}"}
	output := map[string]any{"bad": func() {}}

	result := MakeFunctionCallOutput(call, output, nil)

	if result.FncCallOut != nil {
		t.Fatalf("FncCallOut = %#v, want nil for invalid structured output", result.FncCallOut)
	}
	if result.RawOutput == nil {
		t.Fatal("RawOutput = nil, want original invalid output retained")
	}
	if result.RawError != nil {
		t.Fatalf("RawError = %v, want nil", result.RawError)
	}
}

func TestExecuteFunctionCallReturnsUnknownFunctionOutput(t *testing.T) {
	toolCtx := EmptyToolContext()

	result := ExecuteFunctionCall(context.Background(), &FunctionToolCall{
		Name:   "missing",
		CallID: "call_missing",
	}, toolCtx)

	if result.FncCall.Name != "missing" || result.FncCall.CallID != "call_missing" || result.FncCall.Arguments != "{}" {
		t.Fatalf("FncCall = %#v, want defaulted missing call", result.FncCall)
	}
	if result.FncCallOut == nil {
		t.Fatal("FncCallOut = nil, want unknown function output")
	}
	if !result.FncCallOut.IsError || result.FncCallOut.Output != "Unknown function: missing" {
		t.Fatalf("FncCallOut = %#v, want unknown function error", result.FncCallOut)
	}
	if result.RawError == nil {
		t.Fatal("RawError = nil, want unknown function error")
	}
	if result.RawError.Error() != "Unknown function: missing" {
		t.Fatalf("RawError = %q, want reference unknown function error", result.RawError.Error())
	}
}

func TestExecuteFunctionCallUnknownFunctionPreservesRawArguments(t *testing.T) {
	toolCtx := EmptyToolContext()

	result := ExecuteFunctionCall(context.Background(), &FunctionToolCall{
		Name:      "missing",
		CallID:    "call_missing",
		Arguments: `{city:"Paris",limit:3,}`,
	}, toolCtx)

	if result.FncCall.Name != "missing" || result.FncCall.CallID != "call_missing" {
		t.Fatalf("FncCall identity = %#v, want missing call", result.FncCall)
	}
	if result.FncCall.Arguments != `{city:"Paris",limit:3,}` {
		t.Fatalf("FncCall.Arguments = %q, want raw unparsed arguments", result.FncCall.Arguments)
	}
	if result.FncCallOut == nil {
		t.Fatal("FncCallOut = nil, want unknown function output")
	}
	if !result.FncCallOut.IsError || result.FncCallOut.Output != "Unknown function: missing" {
		t.Fatalf("FncCallOut = %#v, want unknown function error", result.FncCallOut)
	}
	if result.RawError == nil {
		t.Fatal("RawError = nil, want unknown function error")
	}
}

func TestExecuteFunctionCallDefaultsEmptyArgumentsAndReturnsOutput(t *testing.T) {
	tool := &recordingTool{name: "lookup", result: "Paris"}
	toolCtx := NewToolContext([]interface{}{tool})

	result := ExecuteFunctionCall(context.Background(), &FunctionToolCall{
		Name:   "lookup",
		CallID: "call_lookup",
	}, toolCtx)

	if tool.args != "{}" {
		t.Fatalf("tool args = %q, want default JSON object", tool.args)
	}
	if result.FncCall.Arguments != "{}" {
		t.Fatalf("FncCall.Arguments = %q, want default JSON object", result.FncCall.Arguments)
	}
	if result.FncCallOut == nil || result.FncCallOut.IsError || result.FncCallOut.Output != "Paris" {
		t.Fatalf("FncCallOut = %#v, want successful Paris output", result.FncCallOut)
	}
	if result.RawOutput != "Paris" {
		t.Fatalf("RawOutput = %#v, want Paris", result.RawOutput)
	}
}

func TestExecuteFunctionCallDefaultsEmptyExtra(t *testing.T) {
	tool := &recordingTool{name: "lookup", result: "Paris"}
	toolCtx := NewToolContext([]interface{}{tool})

	result := ExecuteFunctionCall(context.Background(), &FunctionToolCall{
		Name:   "lookup",
		CallID: "call_lookup",
	}, toolCtx)

	if result.FncCall.Extra == nil {
		t.Fatal("FncCall.Extra = nil, want mutable empty map")
	}
	if len(result.FncCall.Extra) != 0 {
		t.Fatalf("FncCall.Extra = %#v, want empty map", result.FncCall.Extra)
	}
	result.FncCall.Extra["updated"] = true
	if result.FncCall.Extra["updated"] != true {
		t.Fatalf("FncCall.Extra = %#v, want mutable map", result.FncCall.Extra)
	}
}

func TestExecuteFunctionCallRepairsMalformedArgumentsBeforeExecutingTool(t *testing.T) {
	tool := &recordingTool{name: "lookup", result: "Paris"}
	toolCtx := NewToolContext([]interface{}{tool})

	toolCall := &FunctionToolCall{
		Name:      "lookup",
		CallID:    "call_lookup",
		Arguments: `{city:"Paris",limit:3,}`,
	}
	result := ExecuteFunctionCall(context.Background(), toolCall, toolCtx)

	if tool.args != `{"city":"Paris","limit":3}` {
		t.Fatalf("tool args = %q, want repaired JSON object", tool.args)
	}
	if result.FncCall.Arguments != `{"city":"Paris","limit":3}` {
		t.Fatalf("FncCall.Arguments = %q, want repaired JSON object", result.FncCall.Arguments)
	}
	if toolCall.Arguments != `{"city":"Paris","limit":3}` {
		t.Fatalf("toolCall.Arguments = %q, want repaired JSON object", toolCall.Arguments)
	}
	if result.FncCallOut == nil || result.FncCallOut.IsError || result.FncCallOut.Output != "Paris" {
		t.Fatalf("FncCallOut = %#v, want successful Paris output", result.FncCallOut)
	}
}

func TestExecuteFunctionCallRepairsRawNewlineArgumentsBeforeExecutingTool(t *testing.T) {
	tool := &recordingTool{name: "compose", result: "ok"}
	toolCtx := NewToolContext([]interface{}{tool})

	result := ExecuteFunctionCall(context.Background(), &FunctionToolCall{
		Name:      "compose",
		CallID:    "call_compose",
		Arguments: "{\"message\":\"hello\nworld\"}",
	}, toolCtx)

	if tool.args != `{"message":"hello\nworld"}` {
		t.Fatalf("tool args = %q, want canonical JSON with escaped newline", tool.args)
	}
	if result.FncCall.Arguments != `{"message":"hello\nworld"}` {
		t.Fatalf("FncCall.Arguments = %q, want canonical JSON with escaped newline", result.FncCall.Arguments)
	}
	if result.FncCallOut == nil || result.FncCallOut.IsError {
		t.Fatalf("FncCallOut = %#v, want successful output", result.FncCallOut)
	}
}

func TestExecuteFunctionCallReportsArgumentParseErrorToToolOutput(t *testing.T) {
	tool := &recordingTool{name: "lookup", result: "Paris"}
	toolCtx := NewToolContext([]interface{}{tool})

	result := ExecuteFunctionCall(context.Background(), &FunctionToolCall{
		Name:      "lookup",
		CallID:    "call_lookup",
		Arguments: `["Paris"]`,
	}, toolCtx)

	if tool.args != "" {
		t.Fatalf("tool args = %q, want tool not called after argument parse error", tool.args)
	}
	if result.FncCall.Arguments != `["Paris"]` {
		t.Fatalf("FncCall.Arguments = %q, want raw invalid arguments", result.FncCall.Arguments)
	}
	if result.FncCallOut == nil {
		t.Fatal("FncCallOut = nil, want visible argument parse error")
	}
	want := "Error parsing arguments for `lookup`: expected dict from function arguments, got list: [\"Paris\"]"
	if !result.FncCallOut.IsError || result.FncCallOut.Output != want {
		t.Fatalf("FncCallOut = %#v, want visible parse error %q", result.FncCallOut, want)
	}
	var toolErr ToolError
	if !errors.As(result.RawError, &toolErr) {
		t.Fatalf("RawError = %T, want ToolError", result.RawError)
	}
	if toolErr.Message != want {
		t.Fatalf("ToolError.Message = %q, want %q", toolErr.Message, want)
	}
}

func TestExecuteFunctionCallNormalizesToolError(t *testing.T) {
	tool := &recordingTool{name: "lookup", err: NewToolError("visible failure")}
	toolCtx := NewToolContext([]interface{}{tool})

	result := ExecuteFunctionCall(context.Background(), &FunctionToolCall{
		Name:      "lookup",
		CallID:    "call_lookup",
		Arguments: `{"city":"Paris"}`,
	}, toolCtx)

	if tool.args != `{"city":"Paris"}` {
		t.Fatalf("tool args = %q, want original arguments", tool.args)
	}
	if result.FncCallOut == nil || !result.FncCallOut.IsError || result.FncCallOut.Output != "visible failure" {
		t.Fatalf("FncCallOut = %#v, want visible tool error", result.FncCallOut)
	}
	if result.RawError == nil {
		t.Fatal("RawError = nil, want tool error")
	}
}

func TestExecuteFunctionCallStripsConfirmDuplicateArgument(t *testing.T) {
	tool := &recordingTool{name: "lookup", result: "ok", duplicateMode: ToolDuplicateModeConfirm}
	toolCtx := NewToolContext([]interface{}{tool})

	result := ExecuteFunctionCall(context.Background(), &FunctionToolCall{
		Name:      "lookup",
		CallID:    "call_lookup",
		Arguments: `{"city":"Paris","lk_agents_confirm_duplicate":true}`,
	}, toolCtx)

	if result.RawError != nil {
		t.Fatalf("RawError = %v, want nil", result.RawError)
	}
	if tool.args != `{"city":"Paris"}` {
		t.Fatalf("tool args = %q, want confirmation argument stripped", tool.args)
	}
	if result.FncCall.Arguments != `{"city":"Paris"}` {
		t.Fatalf("FncCall.Arguments = %q, want stripped canonical arguments", result.FncCall.Arguments)
	}
}

func TestCollectStreamAggregatesChunks(t *testing.T) {
	stream := &fakeCollectStream{events: []fakeCollectEvent{
		{chunk: &ChatChunk{
			ID: "req-1",
			Delta: &ChoiceDelta{
				Content: " hello",
				Extra:   map[string]any{"reasoning": "first"},
			},
		}},
		{chunk: &ChatChunk{
			ID: "req-1",
			Delta: &ChoiceDelta{
				Content: " world ",
				ToolCalls: []FunctionToolCall{{
					Type:      "function",
					Name:      "lookup",
					Arguments: `{"city":"Paris"}`,
					CallID:    "call_lookup",
				}},
				Extra: map[string]any{"reasoning": "latest", "trace": "abc"},
			},
		}},
		{chunk: &ChatChunk{
			ID: "req-1",
			Usage: &CompletionUsage{
				CompletionTokens:    3,
				PromptTokens:        5,
				PromptCachedTokens:  2,
				CacheCreationTokens: 1,
				CacheReadTokens:     4,
				TotalTokens:         8,
				ServiceTier:         "priority",
			},
		}},
	}}

	collected, err := CollectStream(stream)
	if err != nil {
		t.Fatalf("CollectStream() error = %v", err)
	}
	if collected.Text != "hello world" {
		t.Fatalf("Text = %q, want trimmed aggregate", collected.Text)
	}
	if len(collected.ToolCalls) != 1 || collected.ToolCalls[0].Name != "lookup" {
		t.Fatalf("ToolCalls = %#v, want lookup call", collected.ToolCalls)
	}
	if collected.Usage == nil || collected.Usage.TotalTokens != 8 {
		t.Fatalf("Usage = %#v, want final usage", collected.Usage)
	}
	if collected.Usage.CacheCreationTokens != 1 || collected.Usage.CacheReadTokens != 4 || collected.Usage.ServiceTier != "priority" {
		t.Fatalf("Usage metadata = %#v, want cache counters and service tier", collected.Usage)
	}
	if collected.Extra["reasoning"] != "latest" || collected.Extra["trace"] != "abc" {
		t.Fatalf("Extra = %#v, want merged latest extra", collected.Extra)
	}
	if !stream.closed {
		t.Fatal("stream was not closed")
	}
}

func TestCollectStreamClosesAndReturnsStreamError(t *testing.T) {
	streamErr := errors.New("stream failed")
	stream := &fakeCollectStream{events: []fakeCollectEvent{{err: streamErr}}}

	collected, err := CollectStream(stream)

	if !errors.Is(err, streamErr) {
		t.Fatalf("CollectStream() error = %v, want stream failure", err)
	}
	if collected != nil {
		t.Fatalf("CollectStream() response = %#v, want nil on error", collected)
	}
	if !stream.closed {
		t.Fatal("stream was not closed after error")
	}
}

func TestCollectStreamRejectsNilStream(t *testing.T) {
	collected, err := CollectStream(nil)

	if err == nil {
		t.Fatal("CollectStream(nil) error = nil, want error")
	}
	if collected != nil {
		t.Fatalf("CollectStream(nil) response = %#v, want nil", collected)
	}
}

func TestCollectStreamRejectsTypedNilStream(t *testing.T) {
	var stream *fakeCollectStream
	collected, err := CollectStream(stream)

	if err == nil {
		t.Fatal("CollectStream(typed nil) error = nil, want error")
	}
	if collected != nil {
		t.Fatalf("CollectStream(typed nil) response = %#v, want nil", collected)
	}
}

func TestTextStreamYieldsOnlyTextDeltasAndCloses(t *testing.T) {
	stream := &fakeCollectStream{events: []fakeCollectEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "hello"}}},
		{chunk: &ChatChunk{Delta: &ChoiceDelta{ToolCalls: []FunctionToolCall{{Name: "lookup"}}}}},
		{chunk: &ChatChunk{Usage: &CompletionUsage{TotalTokens: 2}}},
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: " world"}}},
	}}
	textStream, err := NewTextStream(stream)
	if err != nil {
		t.Fatalf("NewTextStream() error = %v", err)
	}

	first, err := textStream.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	if first != "hello" {
		t.Fatalf("first text = %q, want hello", first)
	}
	second, err := textStream.Next()
	if err != nil {
		t.Fatalf("second Next() error = %v", err)
	}
	if second != " world" {
		t.Fatalf("second text = %q, want world delta", second)
	}
	if _, err := textStream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("final Next() error = %v, want EOF", err)
	}
	if !stream.closed {
		t.Fatal("stream was not closed")
	}
}

func TestTextStreamClosesAndReturnsStreamError(t *testing.T) {
	streamErr := errors.New("stream failed")
	stream := &fakeCollectStream{events: []fakeCollectEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "hello"}}},
		{err: streamErr},
	}}
	textStream, err := NewTextStream(stream)
	if err != nil {
		t.Fatalf("NewTextStream() error = %v", err)
	}

	if text, err := textStream.Next(); err != nil || text != "hello" {
		t.Fatalf("first Next() = (%q, %v), want hello nil", text, err)
	}
	if _, err := textStream.Next(); !errors.Is(err, streamErr) {
		t.Fatalf("second Next() error = %v, want stream failure", err)
	}
	if !stream.closed {
		t.Fatal("stream was not closed after error")
	}
}

func TestNewTextStreamRejectsNilStream(t *testing.T) {
	textStream, err := NewTextStream(nil)

	if err == nil {
		t.Fatal("NewTextStream(nil) error = nil, want error")
	}
	if textStream != nil {
		t.Fatalf("NewTextStream(nil) stream = %#v, want nil", textStream)
	}
}

func TestNewTextStreamRejectsTypedNilStream(t *testing.T) {
	var stream *fakeCollectStream
	textStream, err := NewTextStream(stream)

	if err == nil {
		t.Fatal("NewTextStream(typed nil) error = nil, want error")
	}
	if textStream != nil {
		t.Fatalf("NewTextStream(typed nil) stream = %#v, want nil", textStream)
	}
}

type recordingTool struct {
	name          string
	args          string
	result        string
	err           error
	duplicateMode ToolDuplicateMode
}

func (t *recordingTool) ID() string { return t.name }

func (t *recordingTool) Name() string { return t.name }

func (t *recordingTool) Description() string { return "" }

func (t *recordingTool) Parameters() map[string]any { return nil }

func (t *recordingTool) Execute(_ context.Context, args string) (string, error) {
	t.args = args
	return t.result, t.err
}

func (t *recordingTool) ToolDuplicateMode() ToolDuplicateMode {
	return t.duplicateMode
}

type fakeCollectEvent struct {
	chunk *ChatChunk
	err   error
}

type fakeCollectStream struct {
	events []fakeCollectEvent
	closed bool
}

func (s *fakeCollectStream) Next() (*ChatChunk, error) {
	if len(s.events) == 0 {
		return nil, io.EOF
	}
	event := s.events[0]
	s.events = s.events[1:]
	if event.err != nil {
		return nil, event.err
	}
	return event.chunk, nil
}

func (s *fakeCollectStream) Close() error {
	s.closed = true
	return nil
}
