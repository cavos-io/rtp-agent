package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRunLLMChatContextRunsDeclarativeLookupScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{"name": "ctx", "factory": "llm_chat_context.lookup_fixture"}
		],
		"steps": [
			{"kind": "call", "op": "lookup_by_id", "target": "ctx", "args": {"id": "call"}, "assign": "call_item"},
			{"kind": "call", "op": "lookup_by_id", "target": "ctx", "args": {"id": "missing"}, "assign": "missing_item"},
			{"kind": "call", "op": "index", "target": "ctx", "args": {"id": "call"}, "assign": "call_index"},
			{"kind": "call", "op": "index", "target": "ctx", "args": {"id": "missing"}, "assign": "missing_index"},
			{
				"kind": "emit",
				"name": "lookup_by_id",
				"fields": [
					{"name": "get_call_found", "from": "call_item", "transform": "exists"},
					{"name": "get_missing", "from": "missing_item", "transform": "null_if_missing"},
					{"name": "index_call", "from": "call_index", "transform": "int_or_null"},
					{"name": "index_missing", "from": "missing_index", "transform": "int_or_null"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":           "lookup_by_id",
				"get_call_found": true,
				"get_missing":    nil,
				"index_call":     1,
				"index_missing":  nil,
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeEmptyScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{"name": "ctx", "factory": "llm_chat_context.empty"}
		],
		"steps": [
			{"kind": "call", "op": "count_items", "target": "ctx", "assign": "initial_count"},
			{"kind": "call", "op": "add_message", "target": "ctx", "args": {"id": "msg", "role": "user"}, "assign": "message"},
			{
				"kind": "emit",
				"name": "empty",
				"fields": [
					{"name": "initial_count", "from": "initial_count", "transform": "identity"},
					{"name": "items_is_list", "from": "ctx", "transform": "context_items_is_list"},
					{"name": "append_count", "from": "ctx", "transform": "context_item_count"},
					{"name": "item_ids", "from": "ctx", "transform": "context_item_ids"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":          "empty",
				"initial_count": 0,
				"items_is_list": true,
				"append_count":  1,
				"item_ids":      []string{"msg"},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeCopyFiltersScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{
				"name": "ctx",
				"factory": "llm_chat_context.items",
				"args": {
					"items": [
						{"type": "message", "id": "system", "role": "system", "text": "instructions"},
						{"type": "message", "id": "empty", "role": "user"},
						{"type": "message", "id": "user", "role": "user", "text": "hello"},
						{"type": "function_call", "id": "call", "name": "lookup"},
						{"type": "function_call_output", "id": "output", "name": "lookup"},
						{"type": "agent_handoff", "id": "handoff", "new_agent_id": "next"},
						{"type": "agent_config_update", "id": "config"}
					]
				}
			}
		],
		"steps": [
			{
				"kind": "call",
				"op": "copy",
				"target": "ctx",
				"args": {
					"exclude_function_call": true,
					"exclude_instructions": true,
					"exclude_empty_message": true,
					"exclude_handoff": true,
					"exclude_config_update": true
				},
				"assign": "copied"
			},
			{
				"kind": "emit",
				"name": "copy_filters",
				"fields": [
					{"name": "item_ids", "from": "copied", "transform": "context_item_ids"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":     "copy_filters",
				"item_ids": []string{"user"},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeCopyToolsScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{
				"name": "ctx",
				"factory": "llm_chat_context.items",
				"args": {
					"items": [
						{"type": "message", "id": "user", "role": "user", "text": "hello"},
						{"type": "function_call", "id": "lookup-call", "name": "lookup"},
						{"type": "function_call_output", "id": "lookup-output", "name": "lookup"},
						{"type": "function_call", "id": "calendar-call", "name": "calendar"},
						{"type": "function_call_output", "id": "calendar-output", "name": "calendar"}
					]
				}
			}
		],
		"steps": [
			{
				"kind": "call",
				"op": "copy",
				"target": "ctx",
				"args": {"tools": ["lookup"]},
				"assign": "copied"
			},
			{
				"kind": "emit",
				"name": "copy_excludes_unselected_tools",
				"fields": [
					{"name": "item_ids", "from": "copied", "transform": "context_item_ids"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":     "copy_excludes_unselected_tools",
				"item_ids": []string{"user", "lookup-call", "lookup-output"},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeCopyToolFilterScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{
				"name": "ctx",
				"factory": "llm_chat_context.items",
				"args": {
					"items": [
						{"type": "function_call", "id": "lookup-call", "name": "lookup"},
						{"type": "function_call_output", "id": "lookup-output", "name": "lookup"},
						{"type": "function_call", "id": "weather-call", "name": "weather"},
						{"type": "function_call_output", "id": "weather-output", "name": "weather"},
						{"type": "function_call", "id": "calendar-call", "name": "calendar"},
						{"type": "function_call_output", "id": "calendar-output", "name": "calendar"}
					]
				}
			}
		],
		"steps": [
			{
				"kind": "call",
				"op": "copy",
				"target": "ctx",
				"args": {
					"tools": [
						{"type": "name", "name": "calendar"},
						{"type": "tool", "id": "lookup", "name": "lookup"},
						{"type": "toolset", "id": "tools", "tools": [{"id": "weather", "name": "weather"}]}
					]
				},
				"assign": "copied"
			},
			{
				"kind": "emit",
				"name": "copy_tool_filter",
				"fields": [
					{"name": "item_ids", "from": "copied", "transform": "context_item_ids"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":     "copy_tool_filter",
				"item_ids": []string{"lookup-call", "lookup-output", "weather-call", "weather-output", "calendar-call", "calendar-output"},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeToolNameFlatteningScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{"name": "ctx", "factory": "llm_chat_context.empty"}
		],
		"steps": [
			{
				"kind": "call",
				"op": "tool_names",
				"target": "ctx",
				"args": {
					"tools": [
						{"type": "name", "name": "calendar"},
						{"type": "tool", "id": "lookup", "name": "lookup"},
						{"type": "toolset", "id": "tools", "tools": [{"id": "weather", "name": "weather"}]},
						{"type": "ignored", "value": 123}
					]
				},
				"assign": "names"
			},
			{
				"kind": "emit",
				"name": "tool_name_flattening",
				"fields": [
					{"name": "names", "from": "names", "transform": "identity"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":  "tool_name_flattening",
				"names": []string{"calendar", "lookup", "weather"},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeCopyShallowItemsScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{
				"name": "ctx",
				"factory": "llm_chat_context.items",
				"args": {
					"items": [
						{"type": "message", "id": "user", "role": "user", "text": "hello", "created_at_unix": 10}
					]
				}
			}
		],
		"steps": [
			{"kind": "call", "op": "lookup_by_id", "target": "ctx", "args": {"id": "user"}, "assign": "source_item"},
			{"kind": "call", "op": "copy", "target": "ctx", "assign": "copied"},
			{"kind": "call", "op": "clear_items", "target": "ctx"},
			{
				"kind": "emit",
				"name": "copy_shallow_items",
				"fields": [
					{"name": "copied_count", "from": "copied", "transform": "context_item_count"},
					{"name": "same_item", "from": "copied", "transform": "context_first_identity_matches:source_item"},
					{"name": "source_count_after_clear", "from": "ctx", "transform": "context_item_count"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":                     "copy_shallow_items",
				"copied_count":             1,
				"same_item":                true,
				"source_count_after_clear": 0,
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeAppendConfigDefaultsScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{"name": "ctx", "factory": "llm_chat_context.empty"}
		],
		"steps": [
			{
				"kind": "call",
				"op": "append_item",
				"target": "ctx",
				"args": {"item": {"type": "agent_config_update"}},
				"assign": "config"
			},
			{
				"kind": "emit",
				"name": "append_config_update_defaults",
				"fields": [
					{"name": "id_prefix", "from": "config", "transform": "item_id_has_prefix"},
					{"name": "created_at_set", "from": "config", "transform": "item_created_at_set"},
					{"name": "lookup_found", "from": "ctx", "transform": "context_first_identity_matches:config"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":           "append_config_update_defaults",
				"id_prefix":      true,
				"created_at_set": true,
				"lookup_found":   true,
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeAppendItemDefaultsScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{"name": "ctx", "factory": "llm_chat_context.empty"}
		],
		"steps": [
			{"kind": "call", "op": "append_item", "target": "ctx", "args": {"item": {"type": "message", "role": "user"}}, "assign": "message"},
			{"kind": "call", "op": "append_item", "target": "ctx", "args": {"item": {"type": "function_call", "call_id": "call_lookup", "name": "lookup", "arguments": "{}"}}, "assign": "call"},
			{"kind": "call", "op": "append_item", "target": "ctx", "args": {"item": {"type": "function_call_output", "call_id": "call_lookup", "name": "lookup", "output": "ok"}}, "assign": "output"},
			{"kind": "call", "op": "append_item", "target": "ctx", "args": {"item": {"type": "agent_handoff", "new_agent_id": "next"}}, "assign": "handoff"},
			{
				"kind": "emit",
				"name": "append_item_defaults",
				"fields": [
					{"name": "types", "from": "ctx", "transform": "context_item_types"},
					{"name": "id_prefixes", "from": "ctx", "transform": "context_item_id_prefixes"},
					{"name": "created_at_set", "from": "ctx", "transform": "context_item_created_at_set"},
					{"name": "lookup_found", "from": "ctx", "transform": "context_items_found"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":           "append_item_defaults",
				"types":          []string{"message", "function_call", "function_call_output", "agent_handoff"},
				"id_prefixes":    []bool{true, true, true, true},
				"created_at_set": []bool{true, true, true, true},
				"lookup_found":   []bool{true, true, true, true},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeMergeFiltersScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{
				"name": "base",
				"factory": "llm_chat_context.items",
				"args": {
					"items": [
						{"type": "message", "id": "existing", "role": "user", "text": "hello", "created_at_unix": 10}
					]
				}
			},
			{
				"name": "other",
				"factory": "llm_chat_context.items",
				"args": {
					"items": [
						{"type": "message", "id": "system", "role": "system", "text": "instructions", "created_at_unix": 1},
						{"type": "function_call", "id": "call", "name": "lookup", "created_at_unix": 11},
						{"type": "function_call_output", "id": "output", "name": "lookup", "created_at_unix": 12},
						{"type": "agent_config_update", "id": "config", "created_at_unix": 13},
						{"type": "message", "id": "new", "role": "user", "text": "new", "created_at_unix": 14}
					]
				}
			}
		],
		"steps": [
			{
				"kind": "call",
				"op": "merge",
				"target": "base",
				"args": {
					"other": "other",
					"exclude_function_call": true,
					"exclude_instructions": true,
					"exclude_config_update": true
				},
				"assign": "merged"
			},
			{
				"kind": "emit",
				"name": "merge_filters",
				"fields": [
					{"name": "item_ids", "from": "merged", "transform": "context_item_ids"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":     "merge_filters",
				"item_ids": []string{"existing", "new"},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeAddMessageScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{"name": "ctx", "factory": "llm_chat_context.single_late_message"}
		],
		"steps": [
			{
				"kind": "call",
				"op": "add_message",
				"target": "ctx",
				"args": {
					"id": "early",
					"role": "assistant",
					"text": "early",
					"created_at_unix": 10
				},
				"assign": "message"
			},
			{
				"kind": "emit",
				"name": "add_message_created_at_order",
				"fields": [
					{"name": "message_id", "from": "message", "transform": "item_id"},
					{"name": "role", "from": "message", "transform": "message_role"},
					{"name": "text_content", "from": "message", "transform": "message_text_content"},
					{"name": "item_ids", "from": "ctx", "transform": "context_item_ids"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":         "add_message_created_at_order",
				"message_id":   "early",
				"role":         "assistant",
				"text_content": "early",
				"item_ids":     []string{"early", "late"},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextBuildsDeclarativeMessagesFixture(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{
				"name": "ctx",
				"factory": "llm_chat_context.messages",
				"args": {
					"items": [
						{"id": "existing", "role": "user", "text": "existing", "created_at_unix": 30}
					]
				}
			}
		],
		"steps": [
			{
				"kind": "call",
				"op": "add_message",
				"target": "ctx",
				"args": {"id": "new", "role": "user", "text": "new"},
				"assign": "message"
			},
			{
				"kind": "emit",
				"name": "add_message_default_time",
				"fields": [
					{"name": "created_at_set", "from": "message", "transform": "item_created_at_set"},
					{"name": "item_ids", "from": "ctx", "transform": "context_item_ids"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":           "add_message_default_time",
				"created_at_set": true,
				"item_ids":       []string{"existing", "new"},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeGeneratedIDScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{"name": "ctx", "factory": "llm_chat_context.messages", "args": {"items": []}}
		],
		"steps": [
			{
				"kind": "call",
				"op": "add_message",
				"target": "ctx",
				"args": {"role": "user", "text": "hello"},
				"assign": "message"
			},
			{
				"kind": "emit",
				"name": "add_message_default_id",
				"fields": [
					{"name": "id_prefix", "from": "message", "transform": "item_id_has_prefix"},
					{"name": "stored_same_id", "from": "ctx", "transform": "context_first_id_matches:message"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":           "add_message_default_id",
				"id_prefix":      true,
				"stored_same_id": true,
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeInsertMessagesScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{
				"name": "ctx",
				"factory": "llm_chat_context.messages",
				"args": {
					"items": [
						{"id": "middle", "role": "user", "created_at_unix": 20}
					]
				}
			}
		],
		"steps": [
			{
				"kind": "call",
				"op": "insert_messages",
				"target": "ctx",
				"args": {
					"items": [
						{"id": "late", "role": "user", "created_at_unix": 30},
						{"id": "early", "role": "user", "created_at_unix": 10}
					]
				},
				"assign": "inserted"
			},
			{
				"kind": "emit",
				"name": "insert_created_at_order",
				"fields": [
					{"name": "item_ids", "from": "ctx", "transform": "context_item_ids"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":     "insert_created_at_order",
				"item_ids": []string{"early", "middle", "late"},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeUpsertMessageScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{
				"name": "ctx",
				"factory": "llm_chat_context.messages",
				"args": {
					"items": [
						{"id": "first", "role": "user", "text": "old"},
						{"id": "second", "role": "assistant", "text": "kept"}
					]
				}
			}
		],
		"steps": [
			{
				"kind": "call",
				"op": "upsert_message",
				"target": "ctx",
				"args": {"id": "first", "role": "user", "text": "new"},
				"assign": "updated"
			},
			{
				"kind": "emit",
				"name": "upsert_replaces_id",
				"fields": [
					{"name": "error", "from": "updated_error", "transform": "exists"},
					{"name": "item_ids", "from": "ctx", "transform": "context_item_ids"},
					{"name": "first_is_updated", "from": "ctx", "transform": "context_first_identity_matches:updated"},
					{"name": "first_text", "from": "ctx", "transform": "context_first_message_text_content"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":             "upsert_replaces_id",
				"error":            false,
				"item_ids":         []string{"first", "second"},
				"first_is_updated": true,
				"first_text":       "new",
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeUpsertFunctionCallScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{
				"name": "ctx",
				"factory": "llm_chat_context.messages",
				"args": {
					"items": [
						{"id": "first", "role": "user", "text": "old"}
					]
				}
			}
		],
		"steps": [
			{
				"kind": "call",
				"op": "upsert_function_call",
				"target": "ctx",
				"args": {"id": "call", "call_id": "call_lookup", "name": "lookup", "arguments": "{}"},
				"assign": "inserted"
			},
			{
				"kind": "emit",
				"name": "upsert_appends_missing",
				"fields": [
					{"name": "error", "from": "inserted_error", "transform": "exists"},
					{"name": "item_ids", "from": "ctx", "transform": "context_item_ids"},
					{"name": "inserted_at_end", "from": "ctx", "transform": "context_last_identity_matches:inserted"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":            "upsert_appends_missing",
				"error":           false,
				"item_ids":        []string{"first", "call"},
				"inserted_at_end": true,
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeUpsertTypeMismatchScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{
				"name": "ctx",
				"factory": "llm_chat_context.messages",
				"args": {
					"items": [
						{"id": "item", "role": "user", "text": "old"}
					]
				}
			}
		],
		"steps": [
			{
				"kind": "call",
				"op": "upsert_function_call",
				"target": "ctx",
				"args": {"id": "item", "call_id": "call_lookup", "name": "lookup"},
				"assign": "replacement"
			},
			{
				"kind": "emit",
				"name": "upsert_rejects_type_mismatch",
				"fields": [
					{"name": "error", "from": "replacement_error", "transform": "exists"},
					{"name": "error_message", "from": "replacement_error", "transform": "error_message"},
					{"name": "item_ids", "from": "ctx", "transform": "context_item_ids"},
					{"name": "first_type", "from": "ctx", "transform": "context_first_item_type"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":          "upsert_rejects_type_mismatch",
				"error":         true,
				"error_message": "Item type mismatch: function_call != message",
				"item_ids":      []string{"item"},
				"first_type":    "message",
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeUpsertAllowTypeMismatchScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{
				"name": "ctx",
				"factory": "llm_chat_context.messages",
				"args": {
					"items": [
						{"id": "item", "role": "user", "text": "old"}
					]
				}
			}
		],
		"steps": [
			{
				"kind": "call",
				"op": "upsert_function_call",
				"target": "ctx",
				"args": {
					"id": "item",
					"call_id": "call_lookup",
					"name": "lookup",
					"allow_type_mismatch": true
				},
				"assign": "replacement"
			},
			{
				"kind": "emit",
				"name": "upsert_allows_type_mismatch",
				"fields": [
					{"name": "error", "from": "replacement_error", "transform": "exists"},
					{"name": "first_is_replacement", "from": "ctx", "transform": "context_first_identity_matches:replacement"},
					{"name": "first_type", "from": "ctx", "transform": "context_first_item_type"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":                 "upsert_allows_type_mismatch",
				"error":                false,
				"first_is_replacement": true,
				"first_type":           "function_call",
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeInsertItemScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{"name": "ctx", "factory": "llm_chat_context.empty"}
		],
		"steps": [
			{
				"kind": "call",
				"op": "insert_item",
				"target": "ctx",
				"args": {
					"item": {"type": "agent_config_update"}
				},
				"assign": "config"
			},
			{
				"kind": "emit",
				"name": "insert_config_update_default_id",
				"fields": [
					{"name": "id_prefix", "from": "config", "transform": "item_id_has_prefix"},
					{"name": "lookup_found", "from": "ctx", "transform": "context_first_identity_matches:config"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":         "insert_config_update_default_id",
				"id_prefix":    true,
				"lookup_found": true,
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeUpsertItemScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{"name": "ctx", "factory": "llm_chat_context.empty"}
		],
		"steps": [
			{
				"kind": "call",
				"op": "upsert_item",
				"target": "ctx",
				"args": {
					"item": {"type": "agent_config_update"}
				},
				"assign": "config"
			},
			{
				"kind": "emit",
				"name": "upsert_config_update_defaults",
				"fields": [
					{"name": "id_prefix", "from": "config", "transform": "item_id_has_prefix"},
					{"name": "created_at_set", "from": "config", "transform": "item_created_at_set"},
					{"name": "lookup_found", "from": "ctx", "transform": "context_first_identity_matches:config"},
					{"name": "error", "from": "config_error", "transform": "exists"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":           "upsert_config_update_defaults",
				"id_prefix":      true,
				"created_at_set": true,
				"lookup_found":   true,
				"error":          false,
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeToDictScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{
				"name": "ctx",
				"factory": "llm_chat_context.items",
				"args": {
					"items": [
						{
							"type": "message",
							"id": "message",
							"role": "user",
							"content": [
								"hello",
								{"type": "image_content", "id": "image", "image": "https://example.test/image.png", "inference_detail": "high"},
								{"type": "audio_content", "transcript": "audio text"}
							],
							"created_at_unix": 10
						},
						{"type": "function_call", "id": "call", "call_id": "call_lookup", "name": "lookup", "arguments": "{}", "created_at_unix": 11},
						{"type": "function_call_output", "id": "output", "call_id": "call_lookup", "name": "lookup", "output": "ok", "created_at_unix": 12},
						{"type": "agent_config_update", "id": "config", "created_at_unix": 13}
					]
				}
			}
		],
		"steps": [
			{
				"kind": "call",
				"op": "to_dict",
				"target": "ctx",
				"args": {
					"exclude_function_call": true,
					"exclude_config_update": true
				},
				"assign": "data"
			},
			{
				"kind": "emit",
				"name": "dict_shape",
				"fields": [
					{"name": "data", "from": "data"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name": "dict_shape",
				"data": map[string]any{
					"items": []map[string]any{
						{
							"id":          "message",
							"type":        "message",
							"role":        "user",
							"content":     []any{"hello"},
							"interrupted": false,
							"extra":       map[string]any{},
							"metrics":     map[string]any{},
						},
					},
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeInstructionsScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{
				"name": "ctx",
				"factory": "llm_chat_context.items",
				"args": {
					"items": [
						{
							"type": "message",
							"id": "system",
							"role": "system",
							"content": [
								{"type": "instructions", "audio": "audio instructions", "text": "text instructions"}
							]
						}
					]
				}
			}
		],
		"steps": [
			{"kind": "call", "op": "to_dict", "target": "ctx", "args": {}, "assign": "data"},
			{
				"kind": "emit",
				"name": "instructions_roundtrip",
				"fields": [
					{"name": "serialized_type", "from": "data", "transform": "dict_first_instruction_type"},
					{"name": "serialized_audio", "from": "data", "transform": "dict_first_instruction_audio"},
					{"name": "serialized_text", "from": "data", "transform": "dict_first_instruction_text"},
					{"name": "roundtrip_text", "from": "data", "transform": "dict_first_instruction_text"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":             "instructions_roundtrip",
				"serialized_type":  "instructions",
				"serialized_audio": "audio instructions",
				"serialized_text":  "text instructions",
				"roundtrip_text":   "text instructions",
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}

func TestRunLLMChatContextRunsDeclarativeReadOnlyScenario(t *testing.T) {
	input := json.RawMessage(`{
		"spec_version": "1.0",
		"kind": "parity-scenario",
		"contract": "llm-chat-context",
		"fixtures": [
			{
				"name": "ctx",
				"factory": "llm_chat_context.items",
				"args": {
					"items": [
						{"type": "message", "id": "user", "role": "user", "text": "hello"}
					]
				}
			}
		],
		"steps": [
			{"kind": "call", "op": "read_only", "target": "ctx", "args": {}, "assign": "readonly"},
			{"kind": "call", "op": "add_message_capture_panic", "target": "readonly", "args": {"role": "user", "text": "blocked"}, "assign": "mutation"},
			{"kind": "call", "op": "copy", "target": "readonly", "args": {}, "assign": "mutable"},
			{"kind": "call", "op": "add_message", "target": "mutable", "args": {"id": "copy", "role": "assistant", "text": "ok"}, "assign": "message"},
			{
				"kind": "emit",
				"name": "readonly_view",
				"fields": [
					{"name": "readonly", "from": "readonly", "transform": "context_readonly"},
					{"name": "source_readonly", "from": "ctx", "transform": "context_readonly"},
					{"name": "readonly_ids", "from": "readonly", "transform": "context_item_ids"},
					{"name": "mutation_error", "from": "mutation", "transform": "identity"},
					{"name": "source_ids_after_mutation", "from": "ctx", "transform": "context_item_ids"},
					{"name": "mutable_readonly", "from": "mutable", "transform": "context_readonly"},
					{"name": "mutable_ids", "from": "mutable", "transform": "context_item_ids"},
					{"name": "source_ids_after_copy", "from": "ctx", "transform": "context_item_ids"}
				]
			}
		]
	}`)

	got, err := runLLMChatContext(input)
	if err != nil {
		t.Fatalf("runLLMChatContext() error = %v", err)
	}

	want := map[string]any{
		"contract": "llm-chat-context",
		"events": []map[string]any{
			{
				"name":                      "readonly_view",
				"readonly":                  true,
				"source_readonly":           false,
				"readonly_ids":              []string{"user"},
				"mutation_error":            "trying to modify a read-only chat context, please use .copy() and agent.update_chat_ctx() to modify the chat context",
				"source_ids_after_mutation": []string{"user"},
				"mutable_readonly":          false,
				"mutable_ids":               []string{"user", "copy"},
				"source_ids_after_copy":     []string{"user"},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runLLMChatContext() = %#v, want %#v", got, want)
	}
}
