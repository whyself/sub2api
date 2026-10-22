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

func TestQoderForwardUsesChatAvailabilityInsteadOfCatalogFlag(t *testing.T) {
	for _, model := range []string{"auto", "Qoder/Qwen3.8-Max", "qmodel_38max"} {
		for _, stream := range []bool{false, true} {
			for _, upstreamStatus := range []int{http.StatusOK, http.StatusForbidden} {
				t.Run(fmt.Sprintf("%s/stream=%v/status=%d", model, stream, upstreamStatus), func(t *testing.T) {
					s := NewQoderService(nil, nil, nil)
					account := &Account{Platform: PlatformQoder, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "test", "user_id": "test"}}
					chatRequests := 0
					s.clientFactory = func(string) (*qoder.Client, error) {
						return &qoder.Client{GatewayURL: qoder.Gateway, HTTP: qoderMockDoer(func(req *http.Request) (*http.Response, error) {
							if req.URL.Path == qoder.ModelPath {
								return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"chat":[{"key":"auto","display_name":"Auto","enable":false},{"key":"qmodel_38max","display_name":"Qwen3.8-Max","enable":false}]}`))}, nil
							}
							if req.URL.Path != qoder.ChatPath {
								t.Fatalf("unexpected upstream path: %s", req.URL.Path)
							}
							chatRequests++
							wantKey := "qmodel_38max"
							if model == "auto" {
								wantKey = "auto"
							}
							if req.Header.Get("x-model-key") != wantKey {
								t.Fatalf("wrong upstream model key: %s", req.Header.Get("x-model-key"))
							}
							body := "data: " + `{"choices":[{"index":0,"delta":{"content":"UPSTREAM_OK"},"finish_reason":"stop"}]}` + "\n\n"
							return &http.Response{StatusCode: upstreamStatus, Body: io.NopCloser(strings.NewReader(body))}, nil
						})}, nil
					}
					raw, _ := json.Marshal(map[string]any{"model": model, "messages": []any{map[string]any{"role": "user", "content": "OK"}}})
					response, err := s.Forward(context.Background(), account, raw, stream)
					if err != nil {
						t.Fatal(err)
					}
					defer response.Body.Close()
					body, err := io.ReadAll(response.Body)
					if err != nil {
						t.Fatal(err)
					}
					if response.StatusCode != upstreamStatus || chatRequests != 1 {
						t.Fatalf("catalog flag blocked upstream: status=%d requests=%d body=%s", response.StatusCode, chatRequests, body)
					}
					if upstreamStatus == http.StatusOK && !strings.Contains(string(body), "UPSTREAM_OK") {
						t.Fatalf("upstream response lost: %s", body)
					}
				})
			}
		}
	}
}

func TestQoderResolveModelStillRejectsUnknownModel(t *testing.T) {
	if _, err := qoder.ResolveModel([]qoder.Model{{Key: "auto", DisplayName: "Auto"}}, "unknown-model"); err == nil {
		t.Fatal("unknown model was accepted without a catalog entry")
	}
}
