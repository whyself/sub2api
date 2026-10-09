package qoder

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type testDoer func(*http.Request) (*http.Response, error)

func (f testDoer) Do(req *http.Request) (*http.Response, error) { return f(req) }

func TestQoderPKCEAndVerifierIsNotSerialized(t *testing.T) {
	session, err := BeginDeviceSession()
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(session.AuthURL)
	hash := sha256.Sum256([]byte(session.Verifier))
	if u.Query().Get("challenge") != base64.RawURLEncoding.EncodeToString(hash[:]) {
		t.Fatal("PKCE 校验串不匹配")
	}
	raw, _ := json.Marshal(session)
	if strings.Contains(string(raw), session.Verifier) {
		t.Fatal("秘密校验串进入了公开响应")
	}
}
func TestQoderSignatureBindsEncodedBodyAndPath(t *testing.T) {
	credentials := Credentials{AccessToken: "测试访问令牌", RefreshToken: "测试续期令牌", UserID: "测试用户", MachineID: "测试实例", MachineType: "测试类型", MachineToken: "测试机器令牌"}
	body := Encode([]byte(`{"messages":[{"role":"user","content":"你好"}]}`))
	h, err := SignHeaders(credentials, ChatPath, body)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimPrefix(h.Get("Authorization"), "Bearer COSY."), ".")
	if len(parts) != 2 {
		t.Fatal("认证格式错误")
	}
	digest := md5.Sum([]byte(strings.Join([]string{parts[0], h.Get("cosy-key"), h.Get("cosy-date"), body, strings.TrimPrefix(ChatPath, "/algo")}, "\n")))
	if parts[1] != hex.EncodeToString(digest[:]) {
		t.Fatal("签名没有使用实际编码正文")
	}
	key, err := base64.StdEncoding.DecodeString(h.Get("cosy-key"))
	if err != nil || len(key) != 128 {
		t.Fatal("RSA 包裹格式错误")
	}
}
func TestQoderRefreshAcceptsActualDeviceTokenField(t *testing.T) {
	client := &Client{OpenAPIURL: OpenAPI, HTTP: testDoer(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/v1/deviceToken/refresh" {
			t.Fatalf("续期路径错误：%s", req.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"device_token":"新访问令牌","refresh_token":"新续期令牌","expires_at":"2027-01-01T00:00:00Z"}`))}, nil
	})}
	updated, err := client.Refresh(context.Background(), Credentials{AccessToken: "旧访问令牌", RefreshToken: "旧续期令牌", UserID: "保持身份"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.AccessToken != "新访问令牌" || updated.RefreshToken != "新续期令牌" || updated.UserID != "保持身份" {
		t.Fatal("续期字段或身份被覆盖")
	}
}
func TestQoderStreamDetectsBusinessErrorsAndTruncation(t *testing.T) {
	cases := []string{"data: {\"statusCodeValue\":401,\"body\":\"{}\"}\n\n", "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"未结束\\\"}}]}\"}\n\n"}
	for _, input := range cases {
		if err := ReadChunks(strings.NewReader(input), func(map[string]any) error { return nil }); err == nil {
			t.Fatal("错误流被当成成功")
		}
	}
}
func TestQoderToolArgumentsMergeAcrossInterleavedIndices(t *testing.T) {
	var first, second map[string]any
	_ = json.Unmarshal([]byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":2,"id":"第二个","function":{"name":"clock","arguments":"{"}},{"index":0,"id":"第一个","function":{"name":"weather","arguments":"{\"city\":"}}]}}]}`), &first)
	_ = json.Unmarshal([]byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"南京\"}"}},{"index":2,"function":{"arguments":"}"}}]},"finish_reason":"tool_calls"}]}`), &second)
	collector := &Collector{}
	if err := collector.Add(first); err != nil {
		t.Fatal(err)
	}
	if err := collector.Add(second); err != nil {
		t.Fatal(err)
	}
	result := collector.Completion("qwen3.8-flash")
	message := result["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	calls := message["tool_calls"].([]any)
	if len(calls) != 2 {
		t.Fatal("非连续索引的工具调用被丢弃")
	}
	if calls[0].(map[string]any)["function"].(map[string]any)["arguments"] != `{"city":"南京"}` {
		t.Fatal("分段参数合并错误")
	}
	if (ToolConstraint{Single: true}).Validate(collector) == nil {
		t.Fatal("禁止并行约束没有执行")
	}
}
func TestQoderZeroSeedIsExplicitlyRejected(t *testing.T) {
	_, err := PrepareChat([]byte(`{"messages":[{"role":"user","content":"你好"}],"seed":0}`), Model{Key: "qfmodel"})
	if err == nil {
		t.Fatal("seed=0 被静默丢弃")
	}
}

func TestQoderNonePreservesToolHistoryAndRejectsNewCalls(t *testing.T) {
	request := []byte(`{"tool_choice":"none","tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object"}}}],"messages":[{"role":"assistant","tool_calls":[{"id":"历史调用","type":"function","function":{"name":"weather","arguments":"{}"}}]},{"role":"tool","tool_call_id":"历史调用","content":"23 摄氏度"}]}`)
	body, err := PrepareChat(request, Model{Key: "测试模型"})
	if err != nil {
		t.Fatal(err)
	}
	constraint, err := ApplyToolConstraint(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body["tools"].([]any)) != 1 || len(body["messages"].([]any)) != 3 {
		t.Fatal("工具结果历史或对应定义被丢弃")
	}
	if !constraint.Buffered || constraint.Validate(&Collector{Content: "23 摄氏度"}) != nil {
		t.Fatal("禁止工具调用应缓冲并接受普通结果")
	}
	if constraint.Validate(&Collector{Tools: map[int]*ToolCall{0: {Name: "weather"}}}) == nil {
		t.Fatal("禁止工具调用时不能回传新调用")
	}
}
