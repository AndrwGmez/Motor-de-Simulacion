package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/flowverse/flowverse-api/internal/domain"
)

var ErrMissingValue = errors.New("value does not exist")

func EvaluateCondition(condition domain.Condition, data map[string]any) (bool, error) {
	if len(condition.And) > 0 {
		for _, child := range condition.And {
			ok, err := EvaluateCondition(child, data)
			if err != nil || !ok {
				return ok, err
			}
		}
		return true, nil
	}
	if len(condition.Or) > 0 {
		var lastErr error
		for _, child := range condition.Or {
			ok, err := EvaluateCondition(child, data)
			if err == nil && ok {
				return true, nil
			}
			if err != nil {
				lastErr = err
			}
		}
		if lastErr != nil {
			return false, lastErr
		}
		return false, nil
	}
	if condition.Operator == "and" || condition.Operator == "or" {
		if len(condition.Conditions) == 0 {
			return false, fmt.Errorf("%s requires at least one condition", condition.Operator)
		}
		if condition.Operator == "and" {
			for _, child := range condition.Conditions {
				ok, err := EvaluateCondition(child, data)
				if err != nil || !ok {
					return ok, err
				}
			}
			return true, nil
		}
		for _, child := range condition.Conditions {
			ok, err := EvaluateCondition(child, data)
			if err == nil && ok {
				return true, nil
			}
		}
		return false, nil
	}

	value, err := GetValue(data, condition.Field)
	switch condition.Operator {
	case "exists":
		return err == nil, nil
	case "not_exists":
		return err != nil, nil
	}
	if err != nil {
		return false, err
	}

	switch condition.Operator {
	case "equals":
		return equalStrict(value, condition.Value), nil
	case "not_equals":
		return !equalStrict(value, condition.Value), nil
	case "greater_than", "greater_than_or_equal", "less_than", "less_than_or_equal":
		left, lok := number(value)
		right, rok := number(condition.Value)
		if !lok || !rok {
			return false, fmt.Errorf("%s requires numeric values", condition.Operator)
		}
		switch condition.Operator {
		case "greater_than":
			return left > right, nil
		case "greater_than_or_equal":
			return left >= right, nil
		case "less_than":
			return left < right, nil
		default:
			return left <= right, nil
		}
	case "contains", "not_contains":
		ok, err := contains(value, condition.Value)
		if condition.Operator == "not_contains" {
			ok = !ok
		}
		return ok, err
	default:
		return false, fmt.Errorf("unsupported operator %q", condition.Operator)
	}
}

func equalStrict(left, right any) bool {
	if reflect.TypeOf(left) != reflect.TypeOf(right) {
		// JSON-decoded numbers are all float64. Normalise only numeric Go types.
		ln, lok := number(left)
		rn, rok := number(right)
		return lok && rok && ln == rn
	}
	return reflect.DeepEqual(left, right)
}

func number(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	default:
		return 0, false
	}
}

func contains(container, wanted any) (bool, error) {
	switch value := container.(type) {
	case string:
		text, ok := wanted.(string)
		if !ok {
			return false, errors.New("string contains requires a string value")
		}
		return strings.Contains(value, text), nil
	case []any:
		for _, element := range value {
			if equalStrict(element, wanted) {
				return true, nil
			}
		}
		return false, nil
	case map[string]any:
		key, ok := wanted.(string)
		if !ok {
			return false, errors.New("object contains requires a string key")
		}
		_, ok = value[key]
		return ok, nil
	default:
		return false, fmt.Errorf("contains is not supported for %T", container)
	}
}

func pointerParts(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		// Dotted paths are accepted only as a backwards-compatible input form.
		pointer = "/" + strings.ReplaceAll(pointer, ".", "/")
	}
	segments := strings.Split(pointer[1:], "/")
	for i := range segments {
		segments[i] = strings.ReplaceAll(strings.ReplaceAll(segments[i], "~1", "/"), "~0", "~")
	}
	return segments, nil
}

func GetValue(root any, pointer string) (any, error) {
	parts, err := pointerParts(pointer)
	if err != nil {
		return nil, err
	}
	current := root
	for _, part := range parts {
		switch value := current.(type) {
		case map[string]any:
			next, ok := value[part]
			if !ok {
				return nil, ErrMissingValue
			}
			current = next
		case []any:
			index, parseErr := strconv.Atoi(part)
			if parseErr != nil || index < 0 || index >= len(value) {
				return nil, ErrMissingValue
			}
			current = value[index]
		default:
			return nil, ErrMissingValue
		}
	}
	return current, nil
}

func SetValue(root map[string]any, pointer string, value any) error {
	parts, err := pointerParts(pointer)
	if err != nil || len(parts) == 0 {
		return errors.New("set path must address a field")
	}
	current := root
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part]
		if !ok {
			child := map[string]any{}
			current[part] = child
			current = child
			continue
		}
		child, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("path segment %q is not an object", part)
		}
		current = child
	}
	current[parts[len(parts)-1]] = value
	return nil
}

func DeleteValue(root map[string]any, pointer string) error {
	parts, err := pointerParts(pointer)
	if err != nil || len(parts) == 0 {
		return errors.New("delete path must address a field")
	}
	current := root
	for _, part := range parts[:len(parts)-1] {
		child, ok := current[part].(map[string]any)
		if !ok {
			return ErrMissingValue
		}
		current = child
	}
	if _, ok := current[parts[len(parts)-1]]; !ok {
		return ErrMissingValue
	}
	delete(current, parts[len(parts)-1])
	return nil
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	raw, _ := json.Marshal(value)
	var clone map[string]any
	_ = json.Unmarshal(raw, &clone)
	return clone
}
