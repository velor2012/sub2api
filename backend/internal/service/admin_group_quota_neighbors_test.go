package service

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

// Stateful persistence double: writes and bindings are independent, as in the admin API.
// Derived state follows the repository's eligibility/shared-account rules.
type adminQuotaNeighborsRepo struct {
	GroupRepository
	groups      map[int64]*Group
	bindings    map[int64][]int64
	deleted     map[int64]bool
	snapshotErr error
	bindErr     error
}

func (r *adminQuotaNeighborsRepo) GetByID(_ context.Context, id int64) (*Group, error) {
	group, exists := r.groups[id]
	if !exists || r.deleted[id] {
		return nil, ErrGroupNotFound
	}
	copy := *group
	return &copy, nil
}

func (r *adminQuotaNeighborsRepo) GetByIDLite(ctx context.Context, id int64) (*Group, error) {
	return r.GetByID(ctx, id)
}

func (r *adminQuotaNeighborsRepo) Create(_ context.Context, group *Group) error {
	group.ID = int64(len(r.groups) + 1)
	copy := *group
	r.groups[group.ID] = &copy
	return nil
}

func (r *adminQuotaNeighborsRepo) Update(_ context.Context, group *Group) error {
	copy := *group
	r.groups[group.ID] = &copy
	return nil
}

func (r *adminQuotaNeighborsRepo) GetAccountIDsByGroupIDs(_ context.Context, ids []int64) ([]int64, error) {
	if r.snapshotErr != nil {
		return nil, r.snapshotErr
	}
	var accounts []int64
	for _, id := range ids {
		for _, accountID := range r.bindings[id] {
			if !slices.Contains(accounts, accountID) {
				accounts = append(accounts, accountID)
			}
		}
	}
	return accounts, nil
}

func (r *adminQuotaNeighborsRepo) ListDynamicGroupIDsByAccount(_ context.Context, accountID int64) ([]int64, error) {
	var groups []int64
	for id, group := range r.groups {
		if !r.deleted[id] && group.DynamicQuotaEnabled && group.Platform == PlatformOpenAI && group.IsSubscriptionType() && slices.Contains(r.bindings[id], accountID) {
			groups = append(groups, id)
		}
	}
	return groups, nil
}

func (r *adminQuotaNeighborsRepo) UpdateDynamicQuotaState(ctx context.Context, id int64, estimate *float64) (bool, error) {
	group := r.groups[id]
	group.DynamicQuotaAbnormal = false
	group.DynamicQuotaLimitUSD = nil
	if !group.DynamicQuotaEnabled || group.Platform != PlatformOpenAI || !group.IsSubscriptionType() {
		return true, nil
	}
	for _, accountID := range r.bindings[id] {
		neighbors, _ := r.ListDynamicGroupIDsByAccount(ctx, accountID)
		for _, neighbor := range neighbors {
			if neighbor != id {
				group.DynamicQuotaAbnormal = true
			}
		}
	}
	if !group.DynamicQuotaAbnormal {
		group.DynamicQuotaLimitUSD = estimate // Fixtures have one active position per original group.
	}
	return true, nil
}

func (r *adminQuotaNeighborsRepo) DeleteAccountGroupsByGroupID(_ context.Context, id int64) (int64, error) {
	count := int64(len(r.bindings[id]))
	delete(r.bindings, id)
	return count, nil
}

func (r *adminQuotaNeighborsRepo) BindAccountsToGroup(_ context.Context, id int64, accounts []int64) error {
	if r.bindErr != nil {
		return r.bindErr
	}
	r.bindings[id] = append([]int64(nil), accounts...)
	return nil
}

func (r *adminQuotaNeighborsRepo) DeleteCascade(_ context.Context, id int64) ([]int64, error) {
	if r.deleted[id] || r.groups[id] == nil {
		return nil, ErrGroupNotFound
	}
	r.deleted[id] = true
	delete(r.bindings, id)
	return nil, nil
}

func (r *adminQuotaNeighborsRepo) DeleteCascadeIfEmpty(ctx context.Context, id int64) ([]int64, error) {
	if len(r.bindings[id]) > 0 {
		return nil, ErrGroupNotEmpty
	}
	return r.DeleteCascade(ctx, id)
}

func (r *adminQuotaNeighborsRepo) FindByDuplicateOperationID(_ context.Context, operationID string) (*Group, error) {
	for id, group := range r.groups {
		if !r.deleted[id] && group.DuplicateOperationID == operationID {
			copy := *group
			return &copy, nil
		}
	}
	return nil, nil
}

func (r *adminQuotaNeighborsRepo) CreateFromSource(ctx context.Context, group *Group, sourceID int64) error {
	if err := r.Create(ctx, group); err != nil {
		return err
	}
	r.bindings[group.ID] = append([]int64(nil), r.bindings[sourceID]...)
	return nil
}

func newAdminQuotaNeighborsService() (*adminServiceImpl, *adminQuotaNeighborsRepo) {
	fixed := 100.0
	repo := &adminQuotaNeighborsRepo{
		groups: map[int64]*Group{}, bindings: map[int64][]int64{}, deleted: map[int64]bool{},
	}
	for id := int64(1); id <= 3; id++ {
		repo.groups[id] = &Group{
			ID: id, Name: "group", Platform: PlatformOpenAI, RateMultiplier: 1,
			SubscriptionType: SubscriptionTypeSubscription, DynamicQuotaEnabled: id != 2,
			DailyLimitUSD: &fixed, WeeklyLimitUSD: &fixed,
		}
	}
	repo.bindings[1], repo.bindings[2], repo.bindings[3] = []int64{11}, []int64{11}, []int64{12}
	accounts := &v2AccountRepo{byID: map[int64][]*Account{11: {v2Account(11, true)}, 12: {v2Account(12, true)}}}
	usage := &v2UsageRepo{stats: map[int64]*usagestats.AccountStats{11: v2Stats(12), 12: v2Stats(24)}}
	updater := NewGroupDynamicQuotaUpdater(repo, accounts, usage, nil)
	return &adminServiceImpl{groupRepo: repo, groupDuplicateRepo: repo, emptyGroupDeleteRepo: repo, accountRepo: accounts, dynamicQuotaUpdater: updater}, repo
}

func requireNeighborQuota(t *testing.T, repo *adminQuotaNeighborsRepo, id int64, abnormal bool, limit float64) {
	t.Helper()
	group, err := repo.GetByID(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, abnormal, group.DynamicQuotaAbnormal)
	require.NotNil(t, group.EffectiveDailyLimitUSD())
	require.InDelta(t, limit, *group.EffectiveDailyLimitUSD(), 1e-12)
	require.InDelta(t, limit, *group.EffectiveWeeklyLimitUSD(), 1e-12)
}

func TestAdminGroupQuotaNeighborsEligibilityChanges(t *testing.T) {
	for _, change := range []string{"toggle", "platform", "subscription"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			svc, repo := newAdminQuotaNeighborsService()
			require.NoError(t, svc.dynamicQuotaUpdater.RefreshGroup(ctx, 1))
			requireNeighborQuota(t, repo, 1, false, 30)
			enabled := true
			_, err := svc.UpdateGroup(ctx, 2, &UpdateGroupInput{DynamicQuotaEnabled: &enabled})
			require.NoError(t, err)
			requireNeighborQuota(t, repo, 1, true, 100)
			requireNeighborQuota(t, repo, 2, true, 100)
			input := &UpdateGroupInput{}
			switch change {
			case "toggle":
				enabled = false
				input.DynamicQuotaEnabled = &enabled
			case "platform":
				input.Platform = PlatformAnthropic
			case "subscription":
				input.SubscriptionType = SubscriptionTypeStandard
			}
			_, err = svc.UpdateGroup(ctx, 2, input)
			require.NoError(t, err)
			requireNeighborQuota(t, repo, 1, false, 30)
		})
	}
}

func TestAdminGroupQuotaNeighborsCreateAndDuplicate(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "duplicate"}[duplicate], func(t *testing.T) {
			ctx := context.Background()
			svc, repo := newAdminQuotaNeighborsService()
			require.NoError(t, svc.dynamicQuotaUpdater.RefreshGroup(ctx, 1))
			var group *Group
			var err error
			if duplicate {
				group, err = svc.DuplicateGroup(ctx, 1, "admin:1", "copy")
			} else {
				group, err = svc.CreateGroup(ctx, &CreateGroupInput{
					Name: "copy", Platform: PlatformOpenAI, RateMultiplier: 1,
					SubscriptionType: SubscriptionTypeSubscription, DynamicQuotaEnabled: true,
					CopyAccountsFromGroupIDs: []int64{1},
				})
			}
			require.NoError(t, err)
			require.True(t, group.DynamicQuotaAbnormal)
			requireNeighborQuota(t, repo, 1, true, 100)
			if duplicate {
				retry, retryErr := svc.DuplicateGroup(ctx, 1, "admin:1", "copy")
				require.NoError(t, retryErr)
				require.Equal(t, group.ID, retry.ID)
			}
			require.NoError(t, svc.DeleteGroup(ctx, group.ID))
			requireNeighborQuota(t, repo, 1, false, 30)
		})
	}
}

func TestAdminGroupQuotaNeighborsBindingReplacement(t *testing.T) {
	ctx := context.Background()
	svc, repo := newAdminQuotaNeighborsService()
	enabled := true
	_, err := svc.UpdateGroup(ctx, 2, &UpdateGroupInput{DynamicQuotaEnabled: &enabled})
	require.NoError(t, err)
	require.NoError(t, svc.dynamicQuotaUpdater.RefreshGroup(ctx, 3))
	requireNeighborQuota(t, repo, 1, true, 100)
	requireNeighborQuota(t, repo, 3, false, 60)
	_, err = svc.UpdateGroup(ctx, 2, &UpdateGroupInput{CopyAccountsFromGroupIDs: []int64{3, 3}})
	require.NoError(t, err)
	requireNeighborQuota(t, repo, 1, false, 30)
	requireNeighborQuota(t, repo, 2, true, 100)
	requireNeighborQuota(t, repo, 3, true, 100)
}

func TestAdminGroupQuotaNeighborsPartialReplacementFailure(t *testing.T) {
	ctx := context.Background()
	svc, repo := newAdminQuotaNeighborsService()
	enabled := true
	_, err := svc.UpdateGroup(ctx, 2, &UpdateGroupInput{DynamicQuotaEnabled: &enabled})
	require.NoError(t, err)
	repo.bindErr = errors.New("binding failed")
	_, err = svc.UpdateGroup(ctx, 2, &UpdateGroupInput{CopyAccountsFromGroupIDs: []int64{3}})
	require.ErrorIs(t, err, repo.bindErr)
	requireNeighborQuota(t, repo, 1, false, 30)
}

func TestAdminGroupQuotaNeighborsSnapshotFailurePreventsMutation(t *testing.T) {
	for _, operation := range []string{"create", "update", "delete", "guarded delete", "duplicate"} {
		t.Run(operation, func(t *testing.T) {
			svc, repo := newAdminQuotaNeighborsService()
			repo.snapshotErr = errors.New("snapshot failed")
			ctx := context.Background()
			var err error
			switch operation {
			case "create":
				_, err = svc.CreateGroup(ctx, &CreateGroupInput{
					Name: "copy", Platform: PlatformOpenAI, RateMultiplier: 1,
					SubscriptionType: SubscriptionTypeSubscription, DynamicQuotaEnabled: true,
					CopyAccountsFromGroupIDs: []int64{1},
				})
			case "update":
				enabled := true
				_, err = svc.UpdateGroup(ctx, 2, &UpdateGroupInput{DynamicQuotaEnabled: &enabled})
			case "delete":
				err = svc.DeleteGroup(ctx, 2)
			case "guarded delete":
				err = svc.DeleteGroupIfEmpty(ctx, 2)
			case "duplicate":
				_, err = svc.DuplicateGroup(ctx, 1, "admin:1", "copy")
			}
			require.ErrorIs(t, err, repo.snapshotErr)
			require.False(t, repo.groups[2].DynamicQuotaEnabled)
			require.False(t, repo.deleted[2])
			require.Len(t, repo.groups, 3)
		})
	}
}

func TestAdminGroupQuotaNeighborsGuardedDeletionPreservesBindings(t *testing.T) {
	ctx := context.Background()
	svc, repo := newAdminQuotaNeighborsService()
	enabled := true
	_, err := svc.UpdateGroup(ctx, 2, &UpdateGroupInput{DynamicQuotaEnabled: &enabled})
	require.NoError(t, err)
	require.ErrorIs(t, svc.DeleteGroupIfEmpty(ctx, 2), ErrGroupNotEmpty)
	requireNeighborQuota(t, repo, 1, true, 100)
	require.False(t, repo.deleted[2])
	delete(repo.bindings, 2)
	require.NoError(t, svc.DeleteGroupIfEmpty(ctx, 2))
	require.True(t, repo.deleted[2])
}
