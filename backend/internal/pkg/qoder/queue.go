package qoder

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RateLimitError retains retry metadata without changing the provider error contract.
type RateLimitError struct {
	Message    string
	Code       string
	RetryAfter string
}

func (e *RateLimitError) Error() string { return e.Message }
func (e *RateLimitError) Unwrap() error {
	return &Error{Status: http.StatusTooManyRequests, Message: e.Message}
}

func retryAfter(value string) string {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 && seconds <= 86400 {
		return strconv.Itoa(seconds)
	}
	if date, err := http.ParseTime(value); err == nil && date.After(time.Now()) && time.Until(date) <= 24*time.Hour {
		return date.UTC().Format(http.TimeFormat)
	}
	return ""
}

// Queue details arrive as JSON strings nested inside body/message envelopes.
func queueDetails(value any, depth int) (bool, string) {
	if depth > 6 {
		return false, ""
	}
	if text, ok := value.(string); ok {
		var decoded any
		if json.Unmarshal([]byte(text), &decoded) != nil {
			return false, ""
		}
		return queueDetails(decoded, depth+1)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return false, ""
	}
	found := fmt.Sprint(object["code"]) == "10605"
	after := retryAfter(fmt.Sprint(object["retryAfterSeconds"]))
	for _, key := range []string{"body", "message", "error", "data"} {
		nested, delay := queueDetails(object[key], depth+1)
		found = found || nested
		if after == "" {
			after = delay
		}
	}
	return found, after
}

func queueError(wrapper map[string]any) error {
	found, after := queueDetails(wrapper, 0)
	if !found {
		return nil
	}
	if headers, ok := wrapper["headers"].(map[string]any); ok {
		for name, value := range headers {
			if !strings.EqualFold(name, "Retry-After") {
				continue
			}
			if values, ok := value.([]any); ok && len(values) > 0 {
				value = values[0]
			}
			if delay := retryAfter(fmt.Sprint(value)); delay != "" {
				after = delay
			}
		}
	}
	if after == "" {
		after = "30"
	}
	return &RateLimitError{Message: "Qoder 请求正在排队（10605），请稍后重试", Code: "10605", RetryAfter: after}
}
