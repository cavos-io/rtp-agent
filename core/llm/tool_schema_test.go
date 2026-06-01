package llm

import (
	"reflect"
	"testing"
)

func TestGenerateStrictJSONSchemaRequiresOmitEmptyFieldsAsNullable(t *testing.T) {
	type request struct {
		Query string `json:"query" jsonschema:"search query"`
		Limit int    `json:"limit,omitempty" jsonschema:"maximum results"`
	}

	schema := GenerateStrictJSONSchema(reflect.TypeOf(request{}))

	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("required = %#v, want []string", schema["required"])
	}
	if !reflect.DeepEqual(required, []string{"query", "limit"}) {
		t.Fatalf("required = %#v, want all properties required", required)
	}

	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("properties = %#v, want map", schema["properties"])
	}
	limit, ok := props["limit"].(map[string]interface{})
	if !ok {
		t.Fatalf("limit property = %#v, want map", props["limit"])
	}
	if !reflect.DeepEqual(limit["type"], []string{"integer", "null"}) {
		t.Fatalf("limit type = %#v, want nullable integer", limit["type"])
	}
}

func TestGenerateStrictJSONSchemaSkipsUnexportedFields(t *testing.T) {
	type request struct {
		Query  string `json:"query"`
		secret string `json:"secret"`
	}

	schema := GenerateStrictJSONSchema(reflect.TypeOf(request{}))

	props := schema["properties"].(map[string]interface{})
	if _, ok := props["secret"]; ok {
		t.Fatalf("properties contains unexported field: %#v", props)
	}
	required := schema["required"].([]string)
	if !reflect.DeepEqual(required, []string{"query"}) {
		t.Fatalf("required = %#v, want only exported query", required)
	}
}

func TestGenerateStrictJSONSchemaFlattensAnonymousStructFields(t *testing.T) {
	type Paging struct {
		Limit int `json:"limit"`
	}
	type request struct {
		Paging
		Query string `json:"query"`
	}

	schema := GenerateStrictJSONSchema(reflect.TypeOf(request{}))

	props := schema["properties"].(map[string]interface{})
	if _, ok := props["paging"]; ok {
		t.Fatalf("properties contains embedded struct field instead of flattened fields: %#v", props)
	}
	if _, ok := props["limit"]; !ok {
		t.Fatalf("properties missing flattened limit field: %#v", props)
	}
	required := schema["required"].([]string)
	if !reflect.DeepEqual(required, []string{"limit", "query"}) {
		t.Fatalf("required = %#v, want flattened embedded field order", required)
	}
}

func TestGenerateStrictJSONSchemaKeepsOptionalNestedStructNullable(t *testing.T) {
	type filters struct {
		Status string `json:"status"`
	}
	type request struct {
		Filters *filters `json:"filters,omitempty"`
	}

	schema := GenerateStrictJSONSchema(reflect.TypeOf(request{}))

	props := schema["properties"].(map[string]interface{})
	filtersSchema, ok := props["filters"].(map[string]interface{})
	if !ok {
		t.Fatalf("filters property = %#v, want map", props["filters"])
	}
	if !reflect.DeepEqual(filtersSchema["type"], []string{"object", "null"}) {
		t.Fatalf("filters type = %#v, want nullable object", filtersSchema["type"])
	}
	if filtersSchema["additionalProperties"] != false {
		t.Fatalf("filters additionalProperties = %#v, want false", filtersSchema["additionalProperties"])
	}
	nestedRequired, ok := filtersSchema["required"].([]string)
	if !ok || !reflect.DeepEqual(nestedRequired, []string{"status"}) {
		t.Fatalf("filters required = %#v, want status required", filtersSchema["required"])
	}
}

func TestGenerateStrictJSONSchemaKeepsPointerNestedStructNullable(t *testing.T) {
	type location struct {
		City string `json:"city"`
	}
	type request struct {
		Location *location `json:"location"`
	}

	schema := GenerateStrictJSONSchema(reflect.TypeOf(request{}))

	props := schema["properties"].(map[string]interface{})
	locationSchema, ok := props["location"].(map[string]interface{})
	if !ok {
		t.Fatalf("location property = %#v, want map", props["location"])
	}
	if !reflect.DeepEqual(locationSchema["type"], []string{"object", "null"}) {
		t.Fatalf("location type = %#v, want nullable object for pointer field", locationSchema["type"])
	}
	if locationSchema["additionalProperties"] != false {
		t.Fatalf("location additionalProperties = %#v, want false", locationSchema["additionalProperties"])
	}
	nestedRequired, ok := locationSchema["required"].([]string)
	if !ok || !reflect.DeepEqual(nestedRequired, []string{"city"}) {
		t.Fatalf("location required = %#v, want city required", locationSchema["required"])
	}
}

func TestGenerateStrictJSONSchemaPreservesNestedStructDescription(t *testing.T) {
	type location struct {
		City string `json:"city"`
	}
	type request struct {
		Location *location `json:"location,omitempty" jsonschema:"user location"`
	}

	schema := GenerateStrictJSONSchema(reflect.TypeOf(request{}))

	props := schema["properties"].(map[string]interface{})
	locationSchema, ok := props["location"].(map[string]interface{})
	if !ok {
		t.Fatalf("location property = %#v, want map", props["location"])
	}
	if locationSchema["description"] != "user location" {
		t.Fatalf("location description = %#v, want user location", locationSchema["description"])
	}
}

func TestGenerateStrictJSONSchemaPreservesMapValueSchema(t *testing.T) {
	type request struct {
		Metadata map[string]string `json:"metadata"`
	}

	schema := GenerateStrictJSONSchema(reflect.TypeOf(request{}))

	props := schema["properties"].(map[string]interface{})
	metadataSchema, ok := props["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("metadata property = %#v, want map", props["metadata"])
	}
	valueSchema, ok := metadataSchema["additionalProperties"].(map[string]interface{})
	if !ok {
		t.Fatalf("metadata additionalProperties = %#v, want string value schema", metadataSchema["additionalProperties"])
	}
	if valueSchema["type"] != "string" {
		t.Fatalf("metadata value type = %#v, want string", valueSchema["type"])
	}
}

func TestGenerateStrictJSONSchemaPreservesPointerMapValueSchema(t *testing.T) {
	type request struct {
		Metadata *map[string]int `json:"metadata,omitempty"`
	}

	schema := GenerateStrictJSONSchema(reflect.TypeOf(request{}))

	props := schema["properties"].(map[string]interface{})
	metadataSchema, ok := props["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("metadata property = %#v, want map", props["metadata"])
	}
	if !reflect.DeepEqual(metadataSchema["type"], []string{"object", "null"}) {
		t.Fatalf("metadata type = %#v, want nullable object", metadataSchema["type"])
	}
	valueSchema, ok := metadataSchema["additionalProperties"].(map[string]interface{})
	if !ok {
		t.Fatalf("metadata additionalProperties = %#v, want integer value schema", metadataSchema["additionalProperties"])
	}
	if valueSchema["type"] != "integer" {
		t.Fatalf("metadata value type = %#v, want integer", valueSchema["type"])
	}
}
