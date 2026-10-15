package qoder

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func queueEnvelope(headers any, code any, delay any) []byte {
	encode := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	details := encode(map[string]any{"isQueued": true, "retryAfterSeconds": delay})
	message := encode(map[string]any{"code": code, "message": details})
	return []byte(encode(map[string]any{"statusCodeValue": 403, "headers": headers,
		"body": encode(map[string]any{"code": "403", "message": message})}))
}

func TestQoderQueueIsRetryableAndPreservesDelay(t *testing.T) {
	date := time.Now().Add(time.Minute).UTC().Format(http.TimeFormat)
	for _, tc := range []struct {
		name    string
		headers any
		code    any
		delay   any
		want    string
	}{
		{"actual envelope", map[string]any{"Retry-After": []string{"30"}}, "10605", 30, "30"},
		{"header takes precedence", map[string]any{"retry-after": "45"}, 10605, 30, "45"},
		{"body fallback", nil, "10605", 37, "37"},
		{"HTTP date", map[string]any{"Retry-After": []string{date}}, "10605", 30, date},
		{"invalid metadata", map[string]any{"Retry-After": []string{"invalid"}}, "10605", -1, "30"},
		{"unbounded delay", nil, "10605", 1e100, "30"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := queueEnvelope(tc.headers, tc.code, tc.delay)
			err := ReadChunks(strings.NewReader("data: "+string(payload)+"\n\n"), func(map[string]any) error {
				t.Fatal("queue error emitted a completion")
				return nil
			})
			var limited *RateLimitError
			var provider *Error
			if !errors.As(err, &limited) || !errors.As(err, &provider) || provider.Status != 429 || limited.Code != "10605" || limited.RetryAfter != tc.want {
				t.Fatalf("unexpected queue error: %#v, %v", limited, err)
			}
			if IsUnauthorized(err) {
				t.Fatal("queue triggered credential refresh")
			}
		})
	}
}

func TestQoderQueueDoesNotReplaceAuthenticationErrors(t *testing.T) {
	for _, payload := range [][]byte{
		queueEnvelope(nil, "AUTH_DENIED", 30),
		[]byte(`{"statusCodeValue":403,"body":"not json; 10605 is not a structured code"}`),
		[]byte(`{"statusCodeValue":401,"body":"{}"}`),
		[]byte(`{"body":"{\"code\":\"TOKEN_EXPIRE\"}"}`),
	} {
		_, _, err := Unwrap(payload)
		var limited *RateLimitError
		if err == nil || errors.As(err, &limited) {
			t.Fatalf("authentication error became a queue: %v", err)
		}
	}
	_, _, err := Unwrap([]byte(`{"code":"10605","message":"queued"}`))
	var limited *RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("bare queue error not recognized: %v", err)
	}
}

func TestQoderHTTP429PreservesRetryAfter(t *testing.T) {
	client := &Client{GatewayURL: Gateway, HTTP: testDoer(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"45"}}, Body: io.NopCloser(strings.NewReader("busy"))}, nil
	})}
	err := client.ChatStream(context.Background(), Credentials{AccessToken: "test", UserID: "test"}, map[string]any{}, func(map[string]any) error { return nil })
	var limited *RateLimitError
	if !errors.As(err, &limited) || limited.RetryAfter != "45" {
		t.Fatalf("retry metadata lost: %v", err)
	}
}
