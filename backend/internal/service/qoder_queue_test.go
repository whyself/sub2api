package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/qoder"
)

func TestQoderForwardQueueReturns429BeforeStreamStarts(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			s := NewQoderService(nil, nil, nil)
			account := &Account{Platform: PlatformQoder, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "test", "user_id": "test"}}
			s.clientFactory = func(string) (*qoder.Client, error) {
				return &qoder.Client{GatewayURL: qoder.Gateway, HTTP: qoderMockDoer(func(req *http.Request) (*http.Response, error) {
					body := `{"chat":[{"key":"qfmodel","display_name":"Qwen3.8-Flash","enable":true}]}`
					if req.Method == http.MethodPost {
						body = "data: " + `{"statusCodeValue":403,"headers":{"Retry-After":["30"]},"body":"{\"code\":\"10605\",\"message\":\"queued\"}"}` + "\n\n"
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
				})}, nil
			}
			resp, err := s.Forward(context.Background(), account, []byte(`{"model":"qwen3.8-flash","messages":[{"role":"user","content":"OK"}]}`), stream)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var body map[string]map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != 429 || resp.Header.Get("Retry-After") != "30" || body["error"]["code"] != "10605" || body["error"]["type"] != "rate_limit_error" {
				t.Fatalf("queue response lost: %d %v %v", resp.StatusCode, resp.Header, body)
			}
		})
	}
}
