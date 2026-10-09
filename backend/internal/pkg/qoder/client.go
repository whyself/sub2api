// Package qoder 实现国内版设备授权、COSY 认证及聊天补全协议。
package qoder

import (
	"bufio"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	Website        = "https://qoder.com.cn"
	OpenAPI        = "https://openapi.qoder.com.cn"
	Gateway        = "https://gateway.qoder.com.cn"
	ClientID       = "e883ade2-e6e3-4d6d-adf7-f92ceff5fdcb"
	ClientVersion  = "1.0.10"
	ChatPath       = "/algo/api/v2/service/pro/sse/agent_chat_generation"
	ModelPath      = "/algo/api/v2/model/list"
	customAlphabet = "_doRTgHZBKcGVjlvpC,@aFSx#DPuNJme&i*MzLOEn)sUrthbf%Y^w.(kIQyXqWA!"
	publicKey      = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDA8iMH5c02LilrsERw9t6Pv5Nc
4k6Pz1EaDicBMpdpxKduSZu5OANqUq8er4GM95omAGIOPOh+Nx0spthYA2BqGz+l
6HRkPJ7S236FZz73In/KVuLnwI8JJ2CbuJap8kvheCCZpmAWpb/cPx/3Vr/J6I17
XcW+ML9FoCI6AOvOzwIDAQAB
-----END PUBLIC KEY-----`
)

var ErrPending = errors.New("正在等待官方授权")

type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }
func IsUnauthorized(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusUnauthorized
}

type Credentials struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresAt        string `json:"expires_at"`
	UserID           string `json:"user_id"`
	Name             string `json:"name"`
	Email            string `json:"email"`
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
	UserType         string `json:"user_type"`
	MachineID        string `json:"machine_id"`
	MachineType      string `json:"machine_type"`
	MachineToken     string `json:"machine_token"`
}

func CredentialsFromMap(value map[string]any) Credentials {
	raw, _ := json.Marshal(value)
	var c Credentials
	_ = json.Unmarshal(raw, &c)
	return c
}
func (c Credentials) Map() map[string]any {
	raw, _ := json.Marshal(c)
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	return result
}
func (c Credentials) Expiry() time.Time { t, _ := time.Parse(time.RFC3339Nano, c.ExpiresAt); return t }
func RandomHex(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

type DeviceSession struct {
	Verifier string `json:"-"`
	Nonce    string `json:"-"`
	AuthURL  string `json:"auth_url"`
}

func BeginDeviceSession() (DeviceSession, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return DeviceSession{}, err
	}
	v := base64.RawURLEncoding.EncodeToString(b)
	challenge := sha256.Sum256([]byte(v))
	nonce, err := RandomHex(16)
	if err != nil {
		return DeviceSession{}, err
	}
	query := url.Values{"nonce": {nonce}, "client_id": {ClientID}, "challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "challenge_method": {"S256"}}
	return DeviceSession{Verifier: v, Nonce: nonce, AuthURL: Website + "/device/selectAccounts?" + query.Encode()}, nil
}

type Model struct {
	Key            string         `json:"key"`
	DisplayName    string         `json:"display_name"`
	Enabled        bool           `json:"enable"`
	Vision         bool           `json:"is_vl"`
	Reasoning      bool           `json:"is_reasoning"`
	MaxInputTokens int            `json:"max_input_tokens"`
	ThinkingConfig map[string]any `json:"thinking_config,omitempty"`
}

func (m Model) ID() string {
	if m.DisplayName != "" {
		return strings.ToLower(m.DisplayName)
	}
	return m.Key
}
func ResolveModel(models []Model, name string) (Model, error) {
	norm := func(v string) string {
		return strings.ReplaceAll(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "qoder/"), "_", "-")
	}
	for _, m := range models {
		if norm(name) == norm(m.Key) || norm(name) == norm(m.DisplayName) {
			if !m.Enabled {
				return Model{}, &Error{400, "该模型当前未对账号开放"}
			}
			return m, nil
		}
	}
	return Model{}, &Error{400, "模型不可用，请同步账号模型目录后重试"}
}

type Doer interface {
	Do(*http.Request) (*http.Response, error)
}
type Client struct {
	HTTP                   Doer
	OpenAPIURL, GatewayURL string
}

func NewClient(proxyURL string) (*Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 120 * time.Second
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(u)
	}
	return &Client{HTTP: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, OpenAPIURL: OpenAPI, GatewayURL: Gateway}, nil
}
func (c *Client) jsonCall(ctx context.Context, path string, body any, token string, out any) error {
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(data)
	}
	method := http.MethodGet
	if body != nil {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, c.OpenAPIURL+path, input)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "QoderProtocolBridge/1.0")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.HTTP.Do(req)
	if err != nil {
		return &Error{502, "Qoder 业务接口连接失败"}
	}
	defer response.Body.Close()
	if response.StatusCode == 404 && strings.HasPrefix(path, "/api/v1/deviceToken/poll") {
		return ErrPending
	}
	if response.StatusCode != 200 {
		return &Error{response.StatusCode, fmt.Sprintf("Qoder 接口返回 HTTP %d", response.StatusCode)}
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
	decoder.UseNumber()
	return decoder.Decode(out)
}
func tokenFromResult(data map[string]any) (Credentials, error) {
	get := func(k string) string { v, _ := data[k].(string); return v }
	token := get("device_token")
	if token == "" {
		token = get("token")
	}
	if token == "" {
		return Credentials{}, &Error{401, "授权接口没有返回访问令牌"}
	}
	expires := get("expires_at")
	if expires == "" {
		if n, ok := data["expires_in"].(json.Number); ok {
			milliseconds, _ := n.Int64()
			expires = time.Now().Add(time.Duration(milliseconds) * time.Millisecond).UTC().Format(time.RFC3339)
		}
	}
	return Credentials{AccessToken: token, RefreshToken: get("refresh_token"), ExpiresAt: expires}, nil
}
func (c *Client) Poll(ctx context.Context, session DeviceSession) (Credentials, error) {
	query := url.Values{"nonce": {session.Nonce}, "verifier": {session.Verifier}, "challenge_method": {"S256"}}
	var result map[string]any
	if err := c.jsonCall(ctx, "/api/v1/deviceToken/poll?"+query.Encode(), nil, "", &result); err != nil {
		return Credentials{}, err
	}
	credentials, err := tokenFromResult(result)
	if err != nil {
		return Credentials{}, err
	}
	var user map[string]any
	if err := c.jsonCall(ctx, "/api/v1/userinfo", nil, credentials.AccessToken, &user); err != nil {
		return Credentials{}, err
	}
	str := func(k string) string {
		if v := user[k]; v != nil {
			return fmt.Sprint(v)
		}
		return ""
	}
	credentials.UserID = str("id")
	if credentials.UserID == "" {
		credentials.UserID = str("userId")
	}
	if credentials.UserID == "" {
		return Credentials{}, &Error{502, "用户信息缺少账号标识"}
	}
	credentials.Name = str("name")
	credentials.Email = str("email")
	credentials.OrganizationID = str("organization_id")
	credentials.OrganizationName = str("organization_name")
	credentials.UserType = str("userType")
	if credentials.UserType == "" {
		credentials.UserType = "personal_standard"
	}
	credentials.MachineID, err = RandomHex(16)
	if err != nil {
		return Credentials{}, err
	}
	machineType := sha256.Sum256([]byte("linux-x86_64-qoder-native"))
	credentials.MachineType = hex.EncodeToString(machineType[:])[:18]
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return Credentials{}, err
	}
	credentials.MachineToken = base64.RawURLEncoding.EncodeToString(b)
	return credentials, nil
}
func (c *Client) Refresh(ctx context.Context, old Credentials) (Credentials, error) {
	if old.RefreshToken == "" {
		return Credentials{}, &Error{401, "账号缺少续期令牌，请重新授权"}
	}
	var result map[string]any
	if err := c.jsonCall(ctx, "/api/v1/deviceToken/refresh", map[string]string{"refresh_token": old.RefreshToken}, "", &result); err != nil {
		return Credentials{}, err
	}
	updated, err := tokenFromResult(result)
	if err != nil {
		return Credentials{}, err
	}
	old.AccessToken = updated.AccessToken
	if updated.RefreshToken != "" {
		old.RefreshToken = updated.RefreshToken
	}
	if updated.ExpiresAt != "" {
		old.ExpiresAt = updated.ExpiresAt
	}
	return old, nil
}

func Encode(data []byte) string {
	encoded := base64.StdEncoding.EncodeToString(data)
	a := len(encoded) / 3
	if a > 0 {
		encoded = encoded[len(encoded)-a:] + encoded[a:len(encoded)-a] + encoded[:a]
	}
	standard := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var out strings.Builder
	for _, ch := range encoded {
		if ch == '=' {
			out.WriteByte('$')
		} else {
			out.WriteByte(customAlphabet[strings.IndexRune(standard, ch)])
		}
	}
	return out.String()
}
func SignHeaders(credentials Credentials, path, body string) (http.Header, error) {
	if credentials.AccessToken == "" || credentials.UserID == "" {
		return nil, &Error{401, "账号凭证不完整，请重新授权"}
	}
	keyHex, err := RandomHex(8)
	if err != nil {
		return nil, err
	}
	key := []byte(keyHex)
	block, _ := pem.Decode([]byte(publicKey))
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	wrapped, err := rsa.EncryptPKCS1v15(rand.Reader, parsed.(*rsa.PublicKey), key)
	if err != nil {
		return nil, err
	}
	cosyKey := base64.StdEncoding.EncodeToString(wrapped)
	identity := map[string]string{"uid": credentials.UserID, "aid": credentials.UserID, "name": credentials.Name, "yx_uid": "", "organization_id": credentials.OrganizationID, "organization_name": credentials.OrganizationName, "user_type": credentials.UserType, "security_oauth_token": credentials.AccessToken, "refresh_token": credentials.RefreshToken}
	plain, err := json.Marshal(identity)
	if err != nil {
		return nil, err
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)
	aesBlock, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	encrypted := make([]byte, len(plain))
	cipher.NewCBCEncrypter(aesBlock, key).CryptBlocks(encrypted, plain)
	requestID, err := RandomHex(16)
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]string{"version": "v1", "cosyVersion": ClientVersion, "ideVersion": "", "requestId": requestID, "info": base64.StdEncoding.EncodeToString(encrypted)})
	payloadB64 := base64.StdEncoding.EncodeToString(payload)
	date := strconv.FormatInt(time.Now().Unix(), 10)
	digest := md5.Sum([]byte(strings.Join([]string{payloadB64, cosyKey, date, body, strings.TrimPrefix(path, "/algo")}, "\n")))
	h := http.Header{"Authorization": {"Bearer COSY." + payloadB64 + "." + hex.EncodeToString(digest[:])}, "Content-Type": {"application/json"}, "Accept": {"text/event-stream"}, "User-Agent": {"QoderProtocolBridge/1.0"}}
	for k, v := range map[string]string{"cosy-key": cosyKey, "cosy-date": date, "cosy-user": credentials.UserID, "cosy-version": ClientVersion, "cosy-clienttype": "5", "cosy-data-policy": "agree", "cosy-machineid": credentials.MachineID, "cosy-machinetype": credentials.MachineType, "cosy-machinetoken": credentials.MachineToken, "login-version": "v2", "cosy-scene": "assistant", "cosy-business-product": "ide", "cosy-business-type": "agent"} {
		h.Set(k, v)
	}
	return h, nil
}
func (c *Client) Models(ctx context.Context, credentials Credentials) ([]Model, error) {
	h, err := SignHeaders(credentials, ModelPath, "")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.GatewayURL+ModelPath+"?Encode=1", nil)
	if err != nil {
		return nil, err
	}
	req.Header = h
	response, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, &Error{response.StatusCode, fmt.Sprintf("模型目录返回 HTTP %d", response.StatusCode)}
	}
	var catalog map[string][]Model
	if err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&catalog); err != nil {
		return nil, err
	}
	for _, key := range []string{"chat", "assistant", "developer"} {
		if len(catalog[key]) > 0 {
			return catalog[key], nil
		}
	}
	return nil, &Error{502, "上游没有返回模型目录"}
}

// PrepareChat 保留客户端工具消息，并将尚未支持的关键约束明确拒绝。
func PrepareChat(raw []byte, model Model) (map[string]any, error) {
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, &Error{400, "请求正文必须是 JSON 对象"}
	}
	if input == nil {
		return nil, &Error{400, "请求正文必须是 JSON 对象"}
	}
	if value, ok := input["n"].(float64); ok && value != 1 {
		return nil, &Error{400, "当前只支持 n=1"}
	}
	if input["seed"] != nil {
		return nil, &Error{400, "暂不支持 seed 参数"}
	}
	for _, name := range []string{"stop", "frequency_penalty", "presence_penalty", "logit_bias", "logprobs", "top_logprobs"} {
		v := input[name]
		if v != nil && v != false && v != float64(0) {
			return nil, &Error{400, "尚未验证参数：" + name}
		}
	}
	if format, ok := input["response_format"].(map[string]any); ok && format["type"] != "text" {
		return nil, &Error{400, "暂不支持结构化输出约束"}
	}
	messages, ok := input["messages"].([]any)
	if !ok || len(messages) == 0 {
		return nil, &Error{400, "messages 必须为非空列表"}
	}
	converted := make([]any, 0, len(messages))
	for _, message := range messages {
		m, ok := message.(map[string]any)
		if !ok {
			return nil, &Error{400, "消息必须为对象"}
		}
		switch m["role"] {
		case "system", "user", "assistant", "tool":
		case "developer":
			m["role"] = "system"
		default:
			return nil, &Error{400, "消息角色不受支持"}
		}
		converted = append(converted, m)
	}
	parameters := map[string]any{"max_tokens": 4096}
	if n := input["max_completion_tokens"]; n != nil {
		parameters["max_tokens"] = n
	}
	if n := input["max_tokens"]; n != nil {
		parameters["max_tokens"] = n
	}
	for _, name := range []string{"temperature", "top_p", "reasoning_effort"} {
		if value := input[name]; value != nil {
			parameters[name] = value
		}
	}
	result := map[string]any{"stream": true, "model_config": map[string]any{"key": model.Key, "source": "system", "format": "openai"}, "messages": converted, "parameters": parameters}
	for _, name := range []string{"tools", "tool_choice", "parallel_tool_calls"} {
		if value := input[name]; value != nil {
			result[name] = value
		}
	}
	return result, nil
}

func Unwrap(payload []byte) (map[string]any, bool, error) {
	if string(bytes.TrimSpace(payload)) == "[DONE]" {
		return nil, true, nil
	}
	var wrapper map[string]any
	if err := json.Unmarshal(payload, &wrapper); err != nil {
		return nil, false, err
	}
	if value, ok := wrapper["statusCodeValue"]; ok {
		status := 0
		switch v := value.(type) {
		case float64:
			status = int(v)
		case string:
			status, _ = strconv.Atoi(v)
		}
		if status != 200 {
			return nil, false, &Error{status, fmt.Sprintf("Qoder 流内返回错误状态 %d", status)}
		}
	}
	inner := wrapper
	if body, ok := wrapper["body"]; ok {
		switch v := body.(type) {
		case string:
			if v == "[DONE]" {
				return nil, true, nil
			}
			if err := json.Unmarshal([]byte(v), &inner); err != nil {
				return nil, false, err
			}
		case map[string]any:
			inner = v
		default:
			return nil, false, &Error{502, "上游返回了无法识别的流内容"}
		}
	}
	if code := inner["code"]; code != nil && code != "" && code != "0" && code != float64(0) {
		status := 502
		if fmt.Sprint(code) == "12153" || fmt.Sprint(code) == "TOKEN_EXPIRE" {
			status = 401
		}
		return nil, false, &Error{status, "上游业务错误，代码：" + fmt.Sprint(code)}
	}
	if inner["error"] != nil {
		return nil, false, &Error{502, "上游返回业务错误"}
	}
	terminal := false
	if choices, ok := inner["choices"].([]any); ok {
		for _, choice := range choices {
			if ch, ok := choice.(map[string]any); ok && ch["finish_reason"] != nil && ch["finish_reason"] != "" {
				terminal = true
			}
		}
	}
	return inner, terminal, nil
}

// ReadChunks 按事件边界解包；空流或缺少结束标志均视为失败。
func ReadChunks(reader io.Reader, emit func(map[string]any) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	var parts []string
	seen, terminal := false, false
	flush := func() error {
		if len(parts) == 0 {
			return nil
		}
		chunk, done, err := Unwrap([]byte(strings.Join(parts, "\n")))
		parts = nil
		if err != nil {
			return err
		}
		terminal = terminal || done
		if done && chunk == nil {
			return io.EOF
		}
		if chunk != nil && (chunk["choices"] != nil || chunk["usage"] != nil) {
			seen = true
			return emit(chunk)
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			parts = append(parts, strings.TrimSpace(line[5:]))
		} else if line == "" {
			if err := flush(); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if !seen || !terminal {
		return &Error{502, "上游返回空流或在结束标志前断开"}
	}
	return nil
}

func (c *Client) ChatStream(ctx context.Context, credentials Credentials, body map[string]any, emit func(map[string]any) error) error {
	plain, err := json.Marshal(body)
	if err != nil {
		return err
	}
	encoded := Encode(plain)
	h, err := SignHeaders(credentials, ChatPath, encoded)
	if err != nil {
		return err
	}
	if cfg, ok := body["model_config"].(map[string]any); ok {
		h.Set("x-model-key", fmt.Sprint(cfg["key"]))
	}
	h.Set("x-model-source", "system")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.GatewayURL+ChatPath+"?FetchKeys=llm_model_result&AgentId=agent_common&Encode=1", strings.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header = h
	response, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return &Error{response.StatusCode, fmt.Sprintf("聊天接口返回 HTTP %d", response.StatusCode)}
	}
	return ReadChunks(response.Body, emit)
}
