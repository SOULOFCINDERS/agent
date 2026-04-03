package structured

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// ---------- JSON Schema 定义 ----------

// Schema 表示一个 JSON Schema，用于约束 LLM 输出格式
type Schema struct {
	Name        string          `json:"name"`                  // schema 名称（用于 response_format）
	Description string          `json:"description,omitempty"` // 描述
	Strict      bool            `json:"strict,omitempty"`      // 是否严格模式
	Raw         json.RawMessage `json:"schema"`                // 原始 JSON Schema
	parsed      map[string]any  // 解析后的 schema 缓存
}

// NewSchema 从 JSON Schema 原始字节创建 Schema
func NewSchema(name, description string, rawSchema json.RawMessage) (*Schema, error) {
	var parsed map[string]any
	if err := json.Unmarshal(rawSchema, &parsed); err != nil {
		return nil, fmt.Errorf("invalid JSON Schema: %w", err)
	}
	return &Schema{
		Name:        name,
		Description: description,
		Strict:      true,
		Raw:         rawSchema,
		parsed:      parsed,
	}, nil
}

// NewSchemaFromStruct 从 Go 结构体自动生成 JSON Schema
// 支持 `json` tag 提取字段名，`desc` tag 提取描述
func NewSchemaFromStruct(name, description string, v any) (*Schema, error) {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("expected struct, got %s", t.Kind())
	}

	schema := structToSchema(t)
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}

	return NewSchema(name, description, raw)
}

// structToSchema 递归将 Go struct 类型转换为 JSON Schema map
func structToSchema(t reflect.Type) map[string]any {
	props := make(map[string]any)
	var required []string

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		// 获取 json tag 作为字段名
		jsonTag := field.Tag.Get("json")
		fieldName := field.Name
		omitEmpty := false
		if jsonTag != "" {
			parts := strings.Split(jsonTag, ",")
			if parts[0] == "-" {
				continue
			}
			if parts[0] != "" {
				fieldName = parts[0]
			}
			for _, p := range parts[1:] {
				if p == "omitempty" {
					omitEmpty = true
				}
			}
		}

		// 获取 desc tag 作为描述
		desc := field.Tag.Get("desc")

		// 转换字段类型
		fieldSchema := goTypeToSchema(field.Type)
		if desc != "" {
			fieldSchema["description"] = desc
		}

		props[fieldName] = fieldSchema
		if !omitEmpty {
			required = append(required, fieldName)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	schema["additionalProperties"] = false
	return schema
}

// goTypeToSchema 将 Go 类型映射为 JSON Schema 类型
func goTypeToSchema(t reflect.Type) map[string]any {
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Slice, reflect.Array:
		items := goTypeToSchema(t.Elem())
		return map[string]any{"type": "array", "items": items}
	case reflect.Map:
		return map[string]any{"type": "object"}
	case reflect.Struct:
		return structToSchema(t)
	case reflect.Ptr:
		return goTypeToSchema(t.Elem())
	default:
		return map[string]any{"type": "string"}
	}
}

// ---------- 验证器 ----------

// ValidationError 表示一个验证错误
type ValidationError struct {
	Path    string // JSON path，如 ".items[0].name"
	Message string // 错误描述
}

func (e *ValidationError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

// ValidationResult 验证结果
type ValidationResult struct {
	Valid  bool               `json:"valid"`
	Errors []*ValidationError `json:"errors,omitempty"`
}

// Validate 验证 JSON 数据是否符合 Schema
// 这是一个轻量级验证器，覆盖常见场景：
// - type 检查 (string/number/integer/boolean/array/object/null)
// - required 字段检查
// - properties 递归验证
// - items (数组元素) 验证
// - enum 枚举值检查
// - additionalProperties 检查
func (s *Schema) Validate(data []byte) *ValidationResult {
	if s.parsed == nil {
		var parsed map[string]any
		if err := json.Unmarshal(s.Raw, &parsed); err != nil {
			return &ValidationResult{
				Valid:  false,
				Errors: []*ValidationError{{Message: "schema parse error: " + err.Error()}},
			}
		}
		s.parsed = parsed
	}

	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return &ValidationResult{
			Valid:  false,
			Errors: []*ValidationError{{Message: "invalid JSON: " + err.Error()}},
		}
	}

	var errors []*ValidationError
	validateValue(value, s.parsed, "", &errors)

	return &ValidationResult{
		Valid:  len(errors) == 0,
		Errors: errors,
	}
}

// validateValue 递归验证值是否符合 schema
func validateValue(value any, schema map[string]any, path string, errors *[]*ValidationError) {
	if schema == nil {
		return
	}

	// 1. type 检查
	if expectedType, ok := schema["type"].(string); ok {
		if !checkType(value, expectedType) {
			*errors = append(*errors, &ValidationError{
				Path:    path,
				Message: fmt.Sprintf("expected type %q, got %T", expectedType, value),
			})
			return // type 不对，后续检查无意义
		}
	}

	// 2. enum 枚举检查
	if enumVals, ok := schema["enum"].([]any); ok {
		found := false
		for _, ev := range enumVals {
			if fmt.Sprintf("%v", value) == fmt.Sprintf("%v", ev) {
				found = true
				break
			}
		}
		if !found {
			*errors = append(*errors, &ValidationError{
				Path:    path,
				Message: fmt.Sprintf("value %v not in enum %v", value, enumVals),
			})
		}
	}

	// 3. object 相关检查
	if obj, ok := value.(map[string]any); ok {
		// required 检查
		if reqFields, ok := schema["required"].([]any); ok {
			for _, rf := range reqFields {
				fieldName, _ := rf.(string)
				if _, exists := obj[fieldName]; !exists {
					*errors = append(*errors, &ValidationError{
						Path:    joinPath(path, fieldName),
						Message: "required field missing",
					})
				}
			}
		}

		// properties 递归检查
		if props, ok := schema["properties"].(map[string]any); ok {
			for key, val := range obj {
				if propSchema, ok := props[key].(map[string]any); ok {
					validateValue(val, propSchema, joinPath(path, key), errors)
				} else if addProps, ok := schema["additionalProperties"]; ok {
					if addProps == false {
						*errors = append(*errors, &ValidationError{
							Path:    joinPath(path, key),
							Message: "additional property not allowed",
						})
					}
				}
			}
		}
	}

	// 4. array items 检查
	if arr, ok := value.([]any); ok {
		if itemsSchema, ok := schema["items"].(map[string]any); ok {
			for i, item := range arr {
				itemPath := fmt.Sprintf("%s[%d]", path, i)
				validateValue(item, itemsSchema, itemPath, errors)
			}
		}
	}
}

// checkType 检查 value 是否匹配 JSON Schema 类型
func checkType(value any, expectedType string) bool {
	if value == nil {
		return expectedType == "null"
	}
	switch expectedType {
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, isFloat := value.(float64)
		_, isInt := value.(json.Number)
		return isFloat || isInt
	case "integer":
		f, ok := value.(float64)
		if !ok {
			return false
		}
		return f == float64(int64(f))
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "null":
		return value == nil
	default:
		return true
	}
}

// joinPath 拼接 JSON path
func joinPath(base, field string) string {
	if base == "" {
		return "." + field
	}
	return base + "." + field
}
