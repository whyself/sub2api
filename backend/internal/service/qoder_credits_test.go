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

type creditsRepoStub struct {
	AccountRepository
	mu      sync.Mutex
	account Account
}

func (r *creditsRepoStub) GetByID(context.Context, int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := r.account
	return &copy, nil
}

func newCreditsTestService(doer qoderMockDoer) (*QoderService, *creditsRepoStub, *Account) {
	account := &Account{ID: 2, Name: "测试订阅", Platform: PlatformQoder, Type: AccountTypeOAuth, Status: StatusActive, Credentials: map[string]any{"access_token": "测试身份甲"}}
	repo := &creditsRepoStub{account: *account}
	s := NewQoderService(repo, nil, nil)
	s.clientFactory = func(string) (*qoder.Client, error) { return &qoder.Client{OpenAPIURL: qoder.OpenAPI, HTTP: doer}, nil }
	return s, repo, account
}

func quotaReply(req *http.Request, status int) *http.Response {
	body := `{"usageType":"credits","userQuota":{"total":50,"used":1.5,"remaining":48.5}}`
	if req.URL.Path == "/api/v2/user/plan" {
		body = `{"plan_tier_name":"Teams"}`
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}

func TestQoderCreditsCacheAndFailedRefreshPreserveLastSuccess(t *testing.T) {
	var failing atomic.Bool
	var calls atomic.Int32
	s, _, account := newCreditsTestService(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		status := 200
		if failing.Load() {
			status = 503
		}
		return quotaReply(req, status), nil
	})
	first, err := s.AccountCredits(context.Background(), account, false)
	if err != nil || first.Credits == nil || first.Credits.Subscription.Remaining != 48.5 {
		t.Fatal("首次余额查询失败")
	}
	second, _ := s.AccountCredits(context.Background(), account, false)
	if calls.Load() != 2 || second.State != "cached" {
		t.Fatal("短时缓存未生效")
	}
	key := qoderCreditsKey(account)
	s.mu.Lock()
	entry := s.credits[key]
	entry.attempt = time.Now().Add(-10 * time.Second)
	s.credits[key] = entry
	s.mu.Unlock()
	failing.Store(true)
	stale, err := s.AccountCredits(context.Background(), account, true)
	if err != nil || stale.State != "stale" || stale.Credits.Subscription.Remaining != 48.5 || stale.Error == "" {
		t.Fatal("失败刷新没有保留并标注上次成功余额")
	}
	raw, _ := json.Marshal(stale)
	if strings.Contains(string(raw), "测试身份甲") {
		t.Fatal("额度响应泄露账号凭证")
	}
}

func TestQoderCreditsRejectResultAfterReauthorization(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	s, repo, account := newCreditsTestService(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/api/v2/quota/usage" {
			close(started)
			<-release
		}
		return quotaReply(req, 200), nil
	})
	result := make(chan QoderCreditsAccount)
	go func() { row, _ := s.AccountCredits(context.Background(), account, true); result <- row }()
	<-started
	repo.mu.Lock()
	repo.account.Credentials = map[string]any{"access_token": "测试身份乙"}
	repo.mu.Unlock()
	close(release)
	row := <-result
	if row.Credits != nil || row.State != "error" {
		t.Fatal("重新授权后仍显示旧身份的余额")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.credits) != 0 {
		t.Fatal("旧身份余额进入了缓存")
	}
}

func TestQoderCreditsUnauthorizedWithoutRefresherDoesNotPanic(t *testing.T) {
	s, _, account := newCreditsTestService(func(req *http.Request) (*http.Response, error) { return quotaReply(req, 401), nil })
	row, err := s.AccountCredits(context.Background(), account, true)
	if err != nil || row.Credits != nil || row.State != "error" {
		t.Fatal("授权失败应返回清晰的额度错误状态")
	}
}
