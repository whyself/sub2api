package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/qoder"
	"golang.org/x/sync/singleflight"
)

type QoderCredentialRepository interface {
	UpdateQoderOAuthCredentialsIfUnchanged(context.Context, int64, map[string]any, *int64, map[string]any) (bool, error)
}

type QoderReauthorizationRepository interface {
	ReauthorizeQoderOAuthCredentialsIfUnchanged(context.Context, int64, map[string]any, *int64, map[string]any) (bool, error)
}

type QoderLoginSummary struct {
	SessionID string    `json:"session_id"`
	AuthURL   string    `json:"auth_url,omitempty"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at"`
	AccountID int64     `json:"account_id,omitempty"`
	Email     string    `json:"email,omitempty"`
	Models    []string  `json:"models,omitempty"`
}

type qoderLoginSession struct {
	mu          sync.Mutex
	owner       int64
	id          string
	device      qoder.DeviceSession
	deadline    time.Time
	status      string
	busy        bool
	proxyID     *int64
	targetID    int64
	input       CreateAccountInput
	credentials map[string]any
	models      []string
	accountID   int64
	ctx         context.Context
	cancel      context.CancelFunc
}

func (s *qoderLoginSession) summary() QoderLoginSummary {
	r := QoderLoginSummary{SessionID: s.id, AuthURL: s.device.AuthURL, Status: s.status, ExpiresAt: s.deadline, AccountID: s.accountID, Models: append([]string(nil), s.models...)}
	if s.credentials != nil {
		r.Email, _ = s.credentials["email"].(string)
	}
	return r
}

type qoderModelsCache struct {
	models  []qoder.Model
	expires time.Time
}
type QoderService struct {
	accountRepo   AccountRepository
	proxyRepo     ProxyRepository
	refreshAPI    *OAuthRefreshAPI
	mu            sync.Mutex
	sessions      map[string]*qoderLoginSession
	clients       map[string]*qoder.Client
	models        map[string]qoderModelsCache
	credits       map[string]qoderCreditsCache
	creditsFlight singleflight.Group
	clientFactory func(string) (*qoder.Client, error)
}

func NewQoderService(accounts AccountRepository, proxies ProxyRepository, refreshAPI *OAuthRefreshAPI) *QoderService {
	return &QoderService{accountRepo: accounts, proxyRepo: proxies, refreshAPI: refreshAPI, sessions: make(map[string]*qoderLoginSession), clients: make(map[string]*qoder.Client), models: make(map[string]qoderModelsCache), credits: make(map[string]qoderCreditsCache), clientFactory: qoder.NewClient}
}
func (s *QoderService) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, session := range s.sessions {
		session.cancel()
	}
	s.sessions = make(map[string]*qoderLoginSession)
}
func (s *QoderService) client(ctx context.Context, proxyID *int64) (*qoder.Client, error) {
	proxyURL := ""
	if proxyID != nil {
		if s.proxyRepo == nil {
			return nil, errors.New("Qoder 代理服务未配置")
		}
		proxy, err := s.proxyRepo.GetByID(ctx, *proxyID)
		if err != nil {
			return nil, err
		}
		if proxy == nil {
			return nil, errors.New("指定代理不存在")
		}
		proxyURL = proxy.URL()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if client := s.clients[proxyURL]; client != nil {
		return client, nil
	}
	client, err := s.clientFactory(proxyURL)
	if err == nil {
		s.clients[proxyURL] = client
	}
	return client, err
}
func (s *QoderService) Begin(ctx context.Context, owner int64, input CreateAccountInput, targetID int64) (QoderLoginSummary, error) {
	if owner <= 0 {
		return QoderLoginSummary{}, &qoder.Error{Status: 401, Message: "请先登录管理员账号"}
	}
	if targetID != 0 {
		account, err := s.accountRepo.GetByID(ctx, targetID)
		if err != nil {
			return QoderLoginSummary{}, err
		}
		if account == nil || !account.IsQoder() {
			return QoderLoginSummary{}, &qoder.Error{Status: 400, Message: "重新授权目标不是 Qoder 账号"}
		}
		input.ProxyID = account.ProxyID
	}
	if _, err := s.client(ctx, input.ProxyID); err != nil {
		return QoderLoginSummary{}, err
	}
	device, err := qoder.BeginDeviceSession()
	if err != nil {
		return QoderLoginSummary{}, err
	}
	id, err := qoder.RandomHex(32)
	if err != nil {
		return QoderLoginSummary{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, session := range s.sessions {
		if time.Now().After(session.deadline) {
			session.cancel()
			delete(s.sessions, key)
		}
	}
	if len(s.sessions) >= 100 {
		return QoderLoginSummary{}, &qoder.Error{Status: 429, Message: "待授权会话过多，请稍后重试"}
	}
	deadline := time.Now().Add(10 * time.Minute)
	sessionCtx, cancel := context.WithDeadline(context.Background(), deadline)
	input.Platform = PlatformQoder
	input.Type = AccountTypeOAuth
	input.Credentials = nil
	state := &qoderLoginSession{owner: owner, id: id, device: device, deadline: deadline, status: "pending", proxyID: input.ProxyID, targetID: targetID, input: input, ctx: sessionCtx, cancel: cancel}
	s.sessions[id] = state
	return state.summary(), nil
}
func (s *QoderService) owned(owner int64, id string) (*qoderLoginSession, error) {
	s.mu.Lock()
	session := s.sessions[id]
	s.mu.Unlock()
	if session == nil || session.owner != owner {
		return nil, &qoder.Error{Status: 404, Message: "授权会话不存在"}
	}
	return session, nil
}
func (s *QoderService) Poll(ctx context.Context, owner int64, id string) (QoderLoginSummary, error) {
	session, err := s.owned(owner, id)
	if err != nil {
		return QoderLoginSummary{}, err
	}
	session.mu.Lock()
	if time.Now().After(session.deadline) {
		session.status = "expired"
		session.cancel()
	}
	if session.status != "pending" || session.busy {
		out := session.summary()
		session.mu.Unlock()
		return out, nil
	}
	session.busy = true
	session.mu.Unlock()
	defer func() { session.mu.Lock(); session.busy = false; session.mu.Unlock() }()
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(session.ctx, cancel)
	defer stop()
	client, err := s.client(callCtx, session.proxyID)
	if err != nil {
		return QoderLoginSummary{}, err
	}
	credentials, err := client.Poll(callCtx, session.device)
	if errors.Is(err, qoder.ErrPending) {
		session.mu.Lock()
		defer session.mu.Unlock()
		return session.summary(), nil
	}
	if err != nil {
		return QoderLoginSummary{}, err
	}
	models, err := client.Models(callCtx, credentials)
	if err != nil {
		return QoderLoginSummary{}, err
	}
	mapping := make(map[string]any)
	names := make([]string, 0, len(models))
	for _, model := range models {
		if model.Enabled {
			mapping[model.ID()] = model.ID()
			names = append(names, model.ID())
		}
	}
	result := credentials.Map()
	result["model_mapping"] = mapping
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.status != "pending" || session.ctx.Err() != nil {
		return session.summary(), nil
	}
	session.credentials = result
	session.models = names
	session.status = "ready"
	return session.summary(), nil
}
func (s *QoderService) Cancel(owner int64, id string) (QoderLoginSummary, error) {
	session, err := s.owned(owner, id)
	if err != nil {
		return QoderLoginSummary{}, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.status != "completed" {
		session.status = "cancelled"
		session.credentials = nil
		session.cancel()
	}
	return session.summary(), nil
}
func (s *QoderService) Commit(ctx context.Context, owner int64, id string, creator func(context.Context, *CreateAccountInput) (*Account, error)) (QoderLoginSummary, error) {
	session, err := s.owned(owner, id)
	if err != nil {
		return QoderLoginSummary{}, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.status == "completed" {
		return session.summary(), nil
	}
	if time.Now().After(session.deadline) {
		session.status = "expired"
		session.cancel()
	}
	if session.status != "ready" {
		return session.summary(), &qoder.Error{Status: 409, Message: "授权会话尚未就绪或已经取消"}
	}
	if session.targetID == 0 {
		input := session.input
		input.Credentials = MergeCredentials(nil, session.credentials)
		account, createErr := creator(ctx, &input)
		if createErr != nil {
			return QoderLoginSummary{}, createErr
		}
		session.accountID = account.ID
	} else {
		account, readErr := s.accountRepo.GetByID(ctx, session.targetID)
		if readErr != nil {
			return QoderLoginSummary{}, readErr
		}
		if account == nil || !account.IsQoder() {
			return QoderLoginSummary{}, &qoder.Error{Status: 409, Message: "目标账号状态已变化"}
		}
		if !qoderProxyEqual(account.ProxyID, session.proxyID) {
			return QoderLoginSummary{}, &qoder.Error{Status: 409, Message: "账号代理已变化，请重新发起授权"}
		}
		replacement := MergeCredentials(account.Credentials, session.credentials)
		if account.GetCredential("user_id") == replacement["user_id"] {
			for _, key := range []string{"machine_id", "machine_type", "machine_token"} {
				if value := account.GetCredential(key); value != "" {
					replacement[key] = value
				}
			}
		}
		repo, ok := s.accountRepo.(QoderReauthorizationRepository)
		if !ok {
			return QoderLoginSummary{}, errors.New("Qoder 凭证条件更新服务未配置")
		}
		applied, saveErr := repo.ReauthorizeQoderOAuthCredentialsIfUnchanged(ctx, account.ID, account.Credentials, account.ProxyID, replacement)
		if saveErr != nil {
			return QoderLoginSummary{}, saveErr
		}
		if !applied {
			return QoderLoginSummary{}, &qoder.Error{Status: 409, Message: "账号凭证已变化，请重试本次保存"}
		}
		session.accountID = account.ID
	}
	session.status = "completed"
	session.credentials = nil
	session.device.Verifier = ""
	session.cancel()
	return session.summary(), nil
}
func qoderProxyEqual(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func (s *QoderService) CurrentAccount(ctx context.Context, account *Account, force bool) (*Account, error) {
	if account != nil && account.IsShadow() {
		resolved, err := resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil {
			return nil, err
		}
		account = resolved
	}
	if account == nil || !account.IsQoder() {
		return nil, errors.New("账号不是 Qoder OAuth 账号")
	}
	fresh := account
	if account.ID > 0 {
		var err error
		fresh, err = s.accountRepo.GetByID(ctx, account.ID)
		if err != nil {
			return nil, err
		}
		if fresh == nil || !fresh.IsQoder() {
			return nil, errors.New("Qoder 账号状态已变化")
		}
	}
	credentials := qoder.CredentialsFromMap(fresh.Credentials)
	if force || (!credentials.Expiry().IsZero() && time.Until(credentials.Expiry()) < time.Hour) {
		if s.refreshAPI == nil {
			return nil, errors.New("Qoder 续期服务未配置")
		}
		window := time.Hour
		if force {
			window = 365 * 24 * time.Hour
		}
		result, err := s.refreshAPI.RefreshIfNeeded(ctx, fresh, NewQoderTokenRefresher(s), window)
		if err != nil {
			return nil, err
		}
		if result != nil && result.Account != nil {
			fresh = result.Account
		}
		if result != nil && result.LockHeld {
			latest, err := s.accountRepo.GetByID(ctx, fresh.ID)
			if err != nil {
				return nil, err
			}
			fresh = latest
		}
	}
	if fresh.GetCredential("access_token") == "" {
		return nil, errors.New("Qoder 账号缺少访问令牌")
	}
	return fresh, nil
}
func (s *QoderService) Models(ctx context.Context, account *Account) ([]qoder.Model, error) {
	fresh, err := s.CurrentAccount(ctx, account, false)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(fresh.GetCredential("access_token")))
	key := fmt.Sprintf("%d:%s", fresh.ID, hex.EncodeToString(digest[:8]))
	s.mu.Lock()
	cached, ok := s.models[key]
	s.mu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return append([]qoder.Model(nil), cached.models...), nil
	}
	client, err := s.client(ctx, fresh.ProxyID)
	if err != nil {
		return nil, err
	}
	models, err := client.Models(ctx, qoder.CredentialsFromMap(fresh.Credentials))
	if qoder.IsUnauthorized(err) && fresh.ID > 0 {
		fresh, err = s.CurrentAccount(ctx, fresh, true)
		if err == nil {
			models, err = client.Models(ctx, qoder.CredentialsFromMap(fresh.Credentials))
		}
	}
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	for old, entry := range s.models {
		if time.Now().After(entry.expires) {
			delete(s.models, old)
		}
	}
	s.models[key] = qoderModelsCache{models: models, expires: time.Now().Add(5 * time.Minute)}
	s.mu.Unlock()
	return append([]qoder.Model(nil), models...), nil
}

type QoderTokenRefresher struct{ service *QoderService }

func NewQoderTokenRefresher(service *QoderService) *QoderTokenRefresher {
	return &QoderTokenRefresher{service: service}
}
func (r *QoderTokenRefresher) CacheKey(account *Account) string {
	return fmt.Sprintf("qoder:account:%d", account.ID)
}
func (r *QoderTokenRefresher) CanRefresh(account *Account) bool {
	return account != nil && account.IsQoder() && account.GetCredential("refresh_token") != ""
}
func (r *QoderTokenRefresher) NeedsRefresh(account *Account, window time.Duration) bool {
	expires := account.GetCredentialAsTime("expires_at")
	return expires == nil || time.Until(*expires) < window
}
func (r *QoderTokenRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	if r.service == nil {
		return nil, errors.New("Qoder 续期服务未配置")
	}
	client, err := r.service.client(ctx, account.ProxyID)
	if err != nil {
		return nil, err
	}
	credentials, err := client.Refresh(ctx, qoder.CredentialsFromMap(account.Credentials))
	if err != nil {
		return nil, err
	}
	return MergeCredentials(account.Credentials, credentials.Map()), nil
}
