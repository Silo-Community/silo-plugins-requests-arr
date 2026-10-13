package arr

import (
	"strconv"
	"strings"
)

func BoolOption(options map[string]any, key string, fallback bool) bool {
	value, ok := options[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func StringOption(options map[string]any, key, fallback string) string {
	value, ok := options[key]
	if !ok {
		return fallback
	}
	if typed, ok := value.(string); ok && strings.TrimSpace(typed) != "" {
		return strings.TrimSpace(typed)
	}
	return fallback
}

// IntOption reads an integer option. Config values arrive as JSON numbers
// (float64); admin forms may store a select's choice as its string value.
func IntOption(options map[string]any, key string, fallback int) int {
	switch typed := options[key].(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil {
			return parsed
		}
	}
	return fallback
}
