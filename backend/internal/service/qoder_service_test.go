package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/qoder"
)

type qoderMockDoer func(*http.Request) (*http.Response, error)

func (f qoderMockDoer) Do(r *http.Request) (*http.Response, error) { return f(r) }
func qoderTestService() *QoderService {
	s := NewQoderService(nil, nil, nil)
	s.clientFactory = func(string) (*qoder.Client, error) {
		return &qoder.Client{OpenAPIURL: qoder.OpenAPI, GatewayURL: qoder.Gateway, HTTP: qoderMockDoer(func(req *http.Request) (*http.Response, error) {
			result := any(nil)
			switch req.URL.Path {
			case "/api/v1/deviceToken/poll":
				result = map[string]any{"token": "测试访问-" + req.URL.Query().Get("nonce"), "refresh_token": "测试续期-" + req.URL.Query().Get("nonce"), "expires_at": time.Now().Add(24 * time.Hour).Format(time.RFC3339)}
			case "/api/v1/userinfo":
				result = map[string]any{"id": strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "), "name": "测试账号", "email": "测试账号@示例"}
			case "/algo/api/v2/model/list":
				result = map[string]any{"chat": []any{map[string]any{"key": "qfmodel", "display_name": "Qwen3.8-Flash", "enable": true}}}
			}
			raw, _ := json.Marshal(result)
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
		})}, nil
	}
	return s
}
func TestQoderTwoAuthorizationSessionsRemainIndependent(t *testing.T) {
	s := qoderTestService()
	ctx := context.Background()
	a, _ := s.Begin(ctx, 1, CreateAccountInput{Name: "账号甲"}, 0)
	b, _ := s.Begin(ctx, 1, CreateAccountInput{Name: "账号乙"}, 0)
	if _, err := s.Poll(ctx, 2, a.SessionID); err == nil {
		t.Fatal("其他管理员能够读取授权会话")
	}
	for _, id := range []string{a.SessionID, b.SessionID} {
		if _, err := s.Poll(ctx, 1, id); err != nil {
			t.Fatal(err)
		}
	}
	var saved []*Account
	creator := func(ctx context.Context, input *CreateAccountInput) (*Account, error) {
		account := &Account{ID: int64(len(saved) + 1), Name: input.Name, Platform: input.Platform, Type: input.Type, Credentials: input.Credentials}
		saved = append(saved, account)
		return account, nil
	}
	for _, id := range []string{a.SessionID, b.SessionID} {
		if _, err := s.Commit(ctx, 1, id, creator); err != nil {
			t.Fatal(err)
		}
	}
	if len(saved) != 2 || saved[0].GetCredential("access_token") == saved[1].GetCredential("access_token") || saved[0].GetCredential("machine_id") == saved[1].GetCredential("machine_id") {
		t.Fatal("两个账号共享了凭证或实例状态")
	}
	raw, _ := json.Marshal(a)
	if strings.Contains(string(raw), s.sessions[a.SessionID].device.Verifier) && s.sessions[a.SessionID].device.Verifier != "" {
		t.Fatal("秘密校验串进入响应")
	}
}
func TestQoderCommitIsConsumedOnce(t *testing.T) {
	s := qoderTestService()
	ctx := context.Background()
	session, _ := s.Begin(ctx, 1, CreateAccountInput{Name: "一次保存"}, 0)
	if _, err := s.Poll(ctx, 1, session.SessionID); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	creator := func(context.Context, *CreateAccountInput) (*Account, error) {
		calls.Add(1)
		return &Account{ID: 42}, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := s.Commit(ctx, 1, session.SessionID, creator)
			if err != nil || result.AccountID != 42 {
				t.Errorf("重复保存没有返回原结果：%v", err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("同一授权会话重复创建账号")
	}
}
func TestQoderCancellationAndExpiryCannotCommit(t *testing.T) {
	for _, expired := range []bool{false, true} {
		s := qoderTestService()
		ctx := context.Background()
		session, _ := s.Begin(ctx, 1, CreateAccountInput{Name: "取消测试"}, 0)
		if expired {
			s.sessions[session.SessionID].deadline = time.Now().Add(-time.Second)
		} else {
			_, _ = s.Cancel(1, session.SessionID)
		}
		result, err := s.Poll(ctx, 1, session.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status == "ready" {
			t.Fatal("失效会话仍能授权")
		}
		_, err = s.Commit(ctx, 1, session.SessionID, func(context.Context, *CreateAccountInput) (*Account, error) {
			t.Fatal("失效会话触发账号保存")
			return nil, nil
		})
		if err == nil {
			t.Fatal("失效会话保存成功")
		}
	}
}
