//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestQoderGroupPreservesMessagesDispatchWithoutOpenAIModelDefaults(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := &adminServiceImpl{groupRepo: repo}
	group, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name: "Qoder 测试分组", Platform: PlatformQoder, RateMultiplier: 1,
		AllowMessagesDispatch: true, AllowLive: true, DefaultMappedModel: "gpt-5.4",
	})
	require.NoError(t, err)
	require.True(t, repo.created.AllowMessagesDispatch)
	require.False(t, repo.created.AllowLive)
	require.Empty(t, repo.created.DefaultMappedModel)
	require.Empty(t, group.ResolveMessagesDispatchModel("claude-sonnet-4-6"))
	require.Empty(t, group.ResolveMessagesDispatchModel("qwen3.8-flash"))
}

func TestQoderSimpleModeProvidesMessagesDispatchOnCreateAndUpdate(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := &adminServiceImpl{groupRepo: repo, cfg: &config.Config{RunMode: config.RunModeSimple}}
	group, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name: "Qoder 个人分组", Platform: PlatformQoder,
	})
	require.NoError(t, err)
	require.True(t, group.AllowMessagesDispatch)
	repo.getByID = group
	updated, err := svc.UpdateGroup(context.Background(), group.ID, &UpdateGroupInput{Name: "Qoder 新名称"})
	require.NoError(t, err)
	require.True(t, updated.AllowMessagesDispatch)
}
