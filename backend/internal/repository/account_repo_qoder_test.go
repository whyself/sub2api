package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestQoderCredentialCASScopesPlatformAndPreservesOldVersion(t *testing.T) {
	for _, changed := range []bool{false, true} {
		rows := int64(0)
		if changed {
			rows = 1
		}
		exec := &recordingSQLExecutor{result: rowsAffectedResult(rows)}
		repo := newAccountRepositoryWithSQL(nil, exec, nil)
		proxyID := int64(8)
		applied, err := repo.UpdateQoderOAuthCredentialsIfUnchanged(context.Background(), 42, map[string]any{"refresh_token": "旧续期"}, &proxyID, map[string]any{"refresh_token": "新续期"})
		require.NoError(t, err)
		require.Equal(t, changed, applied)
		require.Len(t, exec.execQueries, 1)
		require.Equal(t, service.PlatformQoder, exec.execArgs[0][2])
		require.Equal(t, &proxyID, exec.execArgs[0][5])
		require.Contains(t, exec.execQueries[0], "a.credentials = $5::jsonb")
		require.Contains(t, exec.execQueries[0], "INSERT INTO scheduler_outbox")
	}
}
func TestQoderReauthorizationRestoresOnlyCredentialErrorState(t *testing.T) {
	exec := &recordingSQLExecutor{result: rowsAffectedResult(1)}
	repo := newAccountRepositoryWithSQL(nil, exec, nil)
	applied, err := repo.ReauthorizeQoderOAuthCredentialsIfUnchanged(context.Background(), 42, map[string]any{"access_token": "旧访问"}, nil, map[string]any{"access_token": "新访问"})
	require.NoError(t, err)
	require.True(t, applied)
	require.Contains(t, exec.execQueries[0], "WHEN a.status = 'error' THEN 'active' ELSE a.status END")
	require.Contains(t, exec.execQueries[0], "token refresh%")
	require.Equal(t, service.PlatformQoder, exec.execArgs[0][2])
}
