//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/qoder"
	"github.com/stretchr/testify/require"
)

func TestQoderQueueCoolsDownWithoutDisablingAccount(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	blocker := &runtimeBlockRecorder{}
	s := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	s.SetAccountRuntimeBlocker(blocker)
	account := &Account{ID: 2, Platform: PlatformQoder, Type: AccountTypeOAuth, Status: StatusActive}
	response := qoderHTTPError(&qoder.RateLimitError{Code: "10605", Message: "queued", RetryAfter: "30"})
	defer response.Body.Close()
	now := time.Now()
	disabled := s.HandleUpstreamError(context.Background(), account, response.StatusCode, response.Header, []byte(`{"error":{"code":"10605","message":"queued"}}`))
	require.False(t, disabled)
	require.Zero(t, repo.setErrorCalls)
	require.Zero(t, repo.tempCalls)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, now.Add(30*time.Second), repo.lastRateLimitedAt, time.Second)
	require.Equal(t, StatusActive, account.Status)
	require.Equal(t, []string{"429"}, blocker.reasons)
}
