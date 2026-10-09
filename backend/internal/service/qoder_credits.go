package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/qoder"
)

const qoderCreditsTTL = 2 * time.Minute
const qoderCreditsRetryDelay = 5 * time.Second
const qoderCreditsRetention = 24 * time.Hour

type QoderCreditsAccount struct {
	ID          int64                  `json:"id"`
	Name        string                 `json:"name"`
	Status      string                 `json:"status"`
	Schedulable bool                   `json:"schedulable"`
	State       string                 `json:"state"`
	Credits     *qoder.CreditsSnapshot `json:"credits,omitempty"`
	Error       string                 `json:"error,omitempty"`
}

type qoderCreditsCache struct {
	snapshot *qoder.CreditsSnapshot
	err      string
	attempt  time.Time
}

func qoderCreditsKey(account *Account) string {
	raw, _ := json.Marshal(struct {
		Credentials map[string]any
		ProxyID     *int64
	}{account.Credentials, account.ProxyID})
	digest := sha256.Sum256(raw)
	return fmt.Sprintf("%d:%s", account.ID, hex.EncodeToString(digest[:]))
}

func creditsAccount(account *Account, cache qoderCreditsCache) QoderCreditsAccount {
	state := "loading"
	if cache.snapshot != nil {
		state = "cached"
	}
	if cache.err != "" {
		state = "error"
		if cache.snapshot != nil {
			state = "stale"
		}
	}
	return QoderCreditsAccount{ID: account.ID, Name: account.Name, Status: account.Status, Schedulable: account.Schedulable, State: state, Credits: cache.snapshot, Error: cache.err}
}

func (s *QoderService) ListCreditsAccounts(ctx context.Context) ([]QoderCreditsAccount, error) {
	accounts, err := s.accountRepo.ListAllWithFilters(ctx, PlatformQoder, AccountTypeOAuth, "", "", 0, "")
	if err != nil {
		return nil, err
	}
	result := make([]QoderCreditsAccount, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if !account.IsQoder() || account.IsShadow() {
			continue
		}
		s.mu.Lock()
		cached := s.credits[qoderCreditsKey(account)]
		s.mu.Unlock()
		if !cached.attempt.IsZero() && time.Since(cached.attempt) > qoderCreditsRetention {
			cached = qoderCreditsCache{}
		}
		row := creditsAccount(account, cached)
		if cached.snapshot != nil && time.Since(cached.snapshot.FetchedAt) > qoderCreditsTTL {
			row.State = "stale"
		}
		result = append(result, row)
	}
	return result, nil
}

func publicCreditsError(err error) string {
	var provider *qoder.Error
	if errors.As(err, &provider) {
		if provider.Status == 401 || provider.Status == 403 {
			return "账号授权失效或没有额度访问权限，请重新授权后重试。"
		}
		if provider.Message == "当前账号未返回 Credits 计量额度" {
			return provider.Message
		}
	}
	return "Qoder 额度查询失败，请稍后重试。"
}

func (s *QoderService) AccountCredits(ctx context.Context, account *Account, force bool) (QoderCreditsAccount, error) {
	fresh, err := s.CurrentAccount(ctx, account, false)
	if err != nil {
		if account == nil || !account.IsQoder() {
			return QoderCreditsAccount{}, err
		}
		return creditsAccount(account, qoderCreditsCache{err: publicCreditsError(err)}), nil
	}
	key := qoderCreditsKey(fresh)
	s.mu.Lock()
	cached := s.credits[key]
	s.mu.Unlock()
	if time.Since(cached.attempt) < qoderCreditsRetryDelay || (!force && cached.err == "" && cached.snapshot != nil && time.Since(cached.snapshot.FetchedAt) < qoderCreditsTTL) {
		return creditsAccount(fresh, cached), nil
	}
	call := s.creditsFlight.DoChan(key, func() (any, error) {
		callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		client, fetchErr := s.client(callCtx, fresh.ProxyID)
		var snapshot *qoder.CreditsSnapshot
		if fetchErr == nil {
			snapshot, fetchErr = client.Credits(callCtx, qoder.CredentialsFromMap(fresh.Credentials))
			if qoder.IsUnauthorized(fetchErr) && fresh.ID > 0 {
				updated, refreshErr := s.CurrentAccount(callCtx, fresh, true)
				fetchErr = refreshErr
				if refreshErr != nil {
					fetchErr = &qoder.Error{Status: 401, Message: "额度授权续期失败"}
				}
				if fetchErr == nil {
					fresh = updated
					client, fetchErr = s.client(callCtx, fresh.ProxyID)
					if fetchErr == nil {
						snapshot, fetchErr = client.Credits(callCtx, qoder.CredentialsFromMap(fresh.Credentials))
					}
				}
			}
		}
		// 重新授权或代理变更后，不能把旧身份的额度发给网页或放入新缓存。
		if fresh.ID > 0 {
			latest, checkErr := s.accountRepo.GetByID(callCtx, fresh.ID)
			if checkErr != nil || latest == nil || !latest.IsQoder() || qoderCreditsKey(latest) != qoderCreditsKey(fresh) {
				return nil, errors.New("账号授权已更新，请重新查询额度")
			}
		}
		entry := qoderCreditsCache{snapshot: snapshot, attempt: time.Now()}
		if fetchErr != nil {
			entry.err = publicCreditsError(fetchErr)
			if qoderCreditsKey(fresh) == key && cached.snapshot != nil && time.Since(cached.snapshot.FetchedAt) < qoderCreditsRetention {
				entry.snapshot = cached.snapshot
			}
		}
		s.mu.Lock()
		for old, value := range s.credits {
			if time.Since(value.attempt) > qoderCreditsRetention {
				delete(s.credits, old)
			}
		}
		s.credits[qoderCreditsKey(fresh)] = entry
		s.mu.Unlock()
		row := creditsAccount(fresh, entry)
		if fetchErr == nil {
			row.State = "fresh"
		}
		return row, nil
	})
	select {
	case <-ctx.Done():
		return QoderCreditsAccount{}, ctx.Err()
	case result := <-call:
		if result.Err != nil {
			return creditsAccount(account, qoderCreditsCache{err: "账号授权或状态已更新，请重新查询额度。"}), nil
		}
		return result.Val.(QoderCreditsAccount), nil
	}
}
