package llm

import (
	"reflect"
	"strings"
)

// GenerateStrictJSONSchema inspects a Go function or struct to generate
// an OpenAI-compatible "strict" JSON schema.
// It enforces additionalProperties: false and makes all fields required
// by default, unless optional tags are provided.
func GenerateStrictJSONSchema(t reflect.Type) map[string]interface{} {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	schema := map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           make(map[string]interface{}),
		"required":             []string{},
	}

	if t.Kind() != reflect.Struct {
		return schema
	}

	props := schema["properties"].(map[string]interface{})
	req := schema["required"].([]string)

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// Get JSON tag, default to field name lowercase
		name := strings.ToLower(field.Name)
		jsonTag := field.Tag.Get("json")
		if jsonTag != "" {
			parts := strings.Split(jsonTag, ",")
			if parts[0] == "-" {
				continue
			}
			if parts[0] != "" {
				name = parts[0]
			}
		}

		desc := field.Tag.Get("jsonschema")
		propSchema := goTypeToJSONSchema(field.Type, desc)

		props[name] = propSchema

		// If omitempty is NOT present in the json tag, the field is required
		if !strings.Contains(jsonTag, "omitempty") {
			req = append(req, name)
		} else {
			// For strict mode, optional fields must be union types with "null"
			if typeArr, ok := propSchema["type"].([]string); ok {
				foundNull := false
				for _, t := range typeArr {
					if t == "null" {
						foundNull = true
						break
					}
				}
				if !foundNull {
					propSchema["type"] = append(typeArr, "null")
				}
			} else if typeStr, ok := propSchema["type"].(string); ok && typeStr != "null" {
				propSchema["type"] = []string{typeStr, "null"}
			}
		}
	}

	schema["required"] = req
	return schema
}

func goTypeToJSONSchema(t reflect.Type, description string) map[string]interface{} {
	schema := map[string]interface{}{}

	if description != "" {
		schema["description"] = description
	}

	if t.Kind() == reflect.Ptr {
		// For pointers, it implies nullability in JSON schema terms
		schema["type"] = []string{goKindToJSONType(t.Elem().Kind()), "null"}
		if t.Elem().Kind() == reflect.Struct {
			// If it's a pointer to a struct, we should describe the struct properties
			// but also allow "null"
			structSchema := GenerateStrictJSONSchema(t.Elem())
			// This is a simplification; a more correct representation might use "oneOf"
			// but for many LLMs this is sufficient.
			return structSchema 
		} else if t.Elem().Kind() == reflect.Slice || t.Elem().Kind() == reflect.Array {
			schema["items"] = goTypeToJSONSchema(t.Elem().Elem(), "")
		}
		return schema
	}
	
	schema["type"] = goKindToJSONType(t.Kind())

	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		schema["items"] = goTypeToJSONSchema(t.Elem(), "")
	case reflect.Struct:
		return GenerateStrictJSONSchema(t)
	case reflect.Map:
		schema["additionalProperties"] = true
	}

	return schema
}

func goKindToJSONType(k reflect.Kind) string {
	switch k {
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Bool:
		return "boolean"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Struct, reflect.Map:
		return "object"
	default:
		return "string" // Fallback
	}
}
