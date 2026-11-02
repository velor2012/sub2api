package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type v2QuotaRepo struct {
	GroupRepository
	group         *Group
	accountIDs    []int64
	dynamicGroups []int64
	updatedSums   map[int64]*float64
	changed       bool
}

func (r *v2QuotaRepo) GetByID(ctx context.Context, id int64) (*Group, error) {
	group := *r.group
	return &group, nil
}

func (r *v2QuotaRepo) GetAccountIDsByGroupIDs(ctx context.Context, groupIDs []int64) ([]int64, error) {
	return r.accountIDs, nil
}

func (r *v2QuotaRepo) ListDynamicGroupIDsByAccount(ctx context.Context, accountID int64) ([]int64, error) {
	return r.dynamicGroups, nil
}

func (r *v2QuotaRepo) UpdateDynamicQuotaState(ctx context.Context, groupID int64, estimateSum *float64, positions int64) (bool, error) {
	r.updatedSums[groupID] = estimateSum
	return r.changed, nil
}

type v2AccountRepo struct {
	AccountRepository
	byID map[int64][]*Account
}

func (r *v2AccountRepo) GetByIDs(ctx context.Context, ids []int64) ([]*Account, error) {
	var out []*Account
	for _, id := range ids {
		out = append(out, r.byID[id]...)
	}
	return out, nil
}

type v2UsageRepo struct {
	UsageLogRepository
	stats map[int64]*usagestats.AccountStats
}

func (r *v2UsageRepo) GetAccountWindowStats(ctx context.Context, accountID int64, startTime time.Time) (*usagestats.AccountStats, error) {
	return r.stats[accountID], nil
}

func v2Account(id int64, withEstimate bool) *Account {
	account := &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"plan_type": "pro"}}
	if withEstimate {
		account.Extra = map[string]any{
			"codex_7d_used_percent": 40.0,
			"codex_7d_reset_at":     "2099-01-01T00:00:00Z",
		}
	}
	return account
}

// v2Stats returns window stats that make estimateOpenAIQuota produce want:
// estimate = cost * 100 / utilization(40) = cost * 2.5.
func v2Stats(cost float64) *usagestats.AccountStats {
	return &usagestats.AccountStats{Cost: cost}
}

func newV2Updater(repo *v2QuotaRepo, accounts map[int64][]*Account, stats map[int64]*usagestats.AccountStats) *GroupDynamicQuotaUpdater {
	return NewGroupDynamicQuotaUpdater(repo, &v2AccountRepo{byID: accounts}, &v2UsageRepo{stats: stats}, nil)
}

func TestV2RefreshGroupSumsAccountEstimates(t *testing.T) {
	ctx := context.Background()
	group := &Group{ID: 1, Platform: PlatformOpenAI, SubscriptionType: SubscriptionTypeSubscription, DynamicQuotaEnabled: true}
	repo := &v2QuotaRepo{group: group, accountIDs: []int64{11, 12}, updatedSums: map[int64]*float64{}}
	accounts := map[int64][]*Account{
		11: {v2Account(11, true)},
		12: {v2Account(12, true)},
	}
	stats := map[int64]*usagestats.AccountStats{11: v2Stats(12), 12: v2Stats(24)}
	updater := newV2Updater(repo, accounts, stats)

	require.NoError(t, updater.RefreshGroup(ctx, 1))
	// estimates: 12*100/40=30, 24*100/40=60 → sum 90
	require.NotNil(t, repo.updatedSums[1])
	require.InDelta(t, 90.0, *repo.updatedSums[1], 1e-12)
}

func TestV2RefreshGroupIgnoresUncomputableAccounts(t *testing.T) {
	ctx := context.Background()
	group := &Group{ID: 1, Platform: PlatformOpenAI, SubscriptionType: SubscriptionTypeSubscription, DynamicQuotaEnabled: true}
	repo := &v2QuotaRepo{group: group, accountIDs: []int64{11, 12, 13}, updatedSums: map[int64]*float64{}}
	accounts := map[int64][]*Account{
		11: {v2Account(11, true)},
		12: {v2Account(12, false)}, // no snapshot → ignored
		13: {v2Account(13, true)},
	}
	stats := map[int64]*usagestats.AccountStats{11: v2Stats(12), 13: v2Stats(0)}
	updater := newV2Updater(repo, accounts, stats)

	require.NoError(t, updater.RefreshGroup(ctx, 1))
	// account 13 has zero cost → estimate nil → ignored; only account 11 counts.
	require.NotNil(t, repo.updatedSums[1])
	require.InDelta(t, 30.0, *repo.updatedSums[1], 1e-12)
}

func TestV2RefreshGroupNoValidEstimateClears(t *testing.T) {
	ctx := context.Background()
	group := &Group{ID: 1, Platform: PlatformOpenAI, SubscriptionType: SubscriptionTypeSubscription, DynamicQuotaEnabled: true}
	repo := &v2QuotaRepo{group: group, accountIDs: []int64{11}, updatedSums: map[int64]*float64{}}
	accounts := map[int64][]*Account{11: {v2Account(11, false)}}
	updater := newV2Updater(repo, accounts, nil)

	require.NoError(t, updater.RefreshGroup(ctx, 1))
	require.Nil(t, repo.updatedSums[1], "no valid estimate must clear dynamic quota")
}

func TestV2RefreshGroupDisabledGroupClears(t *testing.T) {
	ctx := context.Background()
	group := &Group{ID: 1, Platform: PlatformOpenAI, SubscriptionType: SubscriptionTypeSubscription, DynamicQuotaEnabled: false}
	repo := &v2QuotaRepo{group: group, updatedSums: map[int64]*float64{}}
	updater := newV2Updater(repo, nil, nil)

	require.NoError(t, updater.RefreshGroup(ctx, 1))
	require.Nil(t, repo.updatedSums[1], "disabled group must clear dynamic quota")
}

func TestV2RefreshAccountGroupsRefreshesAllDynamicGroups(t *testing.T) {
	ctx := context.Background()
	group := &Group{ID: 1, Platform: PlatformOpenAI, SubscriptionType: SubscriptionTypeSubscription, DynamicQuotaEnabled: true}
	repo := &v2QuotaRepo{group: group, dynamicGroups: []int64{1, 2}, updatedSums: map[int64]*float64{}}
	updater := newV2Updater(repo, nil, nil)

	require.NoError(t, updater.RefreshAccountGroups(ctx, 99))
	require.Contains(t, repo.updatedSums, int64(1))
	require.Contains(t, repo.updatedSums, int64(2))
}
