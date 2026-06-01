package llm

import (
	"reflect"
	"strings"
)

// GenerateStrictJSONSchema inspects a Go function or struct to generate
// an OpenAI-compatible "strict" JSON schema.
// It enforces additionalProperties: false and makes all fields required.
// Optional tags are represented by adding null to the field's type.
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
		if field.PkgPath != "" {
			continue
		}

		// Get JSON tag, default to field name lowercase
		name := strings.ToLower(field.Name)
		jsonTag := field.Tag.Get("json")
		jsonParts := []string{""}
		if jsonTag != "" {
			jsonParts = strings.Split(jsonTag, ",")
			if jsonParts[0] == "-" {
				continue
			}
			if jsonParts[0] != "" {
				name = jsonParts[0]
			}
		}

		if field.Anonymous && jsonParts[0] == "" && indirectKind(field.Type) == reflect.Struct {
			embeddedSchema := GenerateStrictJSONSchema(indirectType(field.Type))
			if embeddedProps, ok := embeddedSchema["properties"].(map[string]interface{}); ok {
				for key, value := range embeddedProps {
					props[key] = value
				}
			}
			if embeddedRequired, ok := embeddedSchema["required"].([]string); ok {
				for _, requiredField := range embeddedRequired {
					req = appendRequiredField(req, requiredField)
				}
			}
			continue
		}

		desc := field.Tag.Get("jsonschema")
		propSchema := goTypeToJSONSchema(field.Type, desc)
		if enumValues := jsonSchemaEnumValues(field.Tag.Get("enum")); len(enumValues) > 0 {
			propSchema["enum"] = enumValues
			if schemaTypeIncludesNull(propSchema) {
				markSchemaEnumNullable(propSchema)
			}
		}

		props[name] = propSchema

		req = appendRequiredField(req, name)

		if strings.Contains(jsonTag, "omitempty") || field.Tag.Get("default") != "" {
			markSchemaNullable(propSchema)
		}
	}

	schema["required"] = req
	return schema
}

func jsonSchemaEnumValues(tag string) []any {
	if tag == "" {
		return nil
	}
	parts := strings.Split(tag, ",")
	values := make([]any, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		values = append(values, value)
	}
	return values
}

func schemaTypeIncludesNull(schema map[string]interface{}) bool {
	typeArr, ok := schema["type"].([]string)
	if !ok {
		return false
	}
	for _, t := range typeArr {
		if t == "null" {
			return true
		}
	}
	return false
}

func markSchemaNullable(schema map[string]interface{}) {
	if typeArr, ok := schema["type"].([]string); ok {
		for _, t := range typeArr {
			if t == "null" {
				markSchemaEnumNullable(schema)
				return
			}
		}
		schema["type"] = append(typeArr, "null")
		markSchemaEnumNullable(schema)
		return
	}
	if typeStr, ok := schema["type"].(string); ok && typeStr != "null" {
		schema["type"] = []string{typeStr, "null"}
		markSchemaEnumNullable(schema)
	}
}

func markSchemaEnumNullable(schema map[string]interface{}) {
	enumValues, ok := schema["enum"].([]any)
	if !ok {
		return
	}
	for _, value := range enumValues {
		if value == nil {
			return
		}
	}
	schema["enum"] = append(enumValues, nil)
}

func appendRequiredField(required []string, name string) []string {
	for _, existing := range required {
		if existing == name {
			return required
		}
	}
	return append(required, name)
}

func indirectType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
}

func indirectKind(t reflect.Type) reflect.Kind {
	return indirectType(t).Kind()
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
			// but also allow "null".
			structSchema := GenerateStrictJSONSchema(t.Elem())
			structSchema["type"] = []string{"object", "null"}
			if description != "" {
				structSchema["description"] = description
			}
			return structSchema
		} else if t.Elem().Kind() == reflect.Slice || t.Elem().Kind() == reflect.Array {
			schema["items"] = goTypeToJSONSchema(t.Elem().Elem(), "")
		} else if t.Elem().Kind() == reflect.Map {
			schema["additionalProperties"] = goTypeToJSONSchema(t.Elem().Elem(), "")
		}
		return schema
	}

	schema["type"] = goKindToJSONType(t.Kind())

	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		schema["items"] = goTypeToJSONSchema(t.Elem(), "")
	case reflect.Struct:
		structSchema := GenerateStrictJSONSchema(t)
		if description != "" {
			structSchema["description"] = description
		}
		return structSchema
	case reflect.Map:
		schema["additionalProperties"] = goTypeToJSONSchema(t.Elem(), "")
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
