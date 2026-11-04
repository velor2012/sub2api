package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"
)

// GroupDynamicQuotaUpdater updates persisted quotas at existing account refresh points.
type GroupDynamicQuotaUpdater struct {
	groupRepo   GroupRepository
	accountRepo AccountRepository
	usageRepo   UsageLogRepository
	invalidator APIKeyAuthCacheInvalidator
}

func NewGroupDynamicQuotaUpdater(groupRepo GroupRepository, accountRepo AccountRepository, usageRepo UsageLogRepository, invalidator APIKeyAuthCacheInvalidator) *GroupDynamicQuotaUpdater {
	return &GroupDynamicQuotaUpdater{groupRepo: groupRepo, accountRepo: accountRepo, usageRepo: usageRepo, invalidator: invalidator}
}

// estimateOpenAIQuota preserves the account page's estimated total cost formula.
func estimateOpenAIQuota(window *UsageProgress) *float64 {
	if window == nil || window.WindowStats == nil {
		return nil
	}
	cost, utilization := window.WindowStats.Cost, window.Utilization
	if cost <= 0 || utilization <= 0 || math.IsNaN(cost) || math.IsInf(cost, 0) || math.IsNaN(utilization) || math.IsInf(utilization, 0) {
		return nil
	}
	estimate := cost * 100 / utilization
	if estimate <= 0 || math.IsNaN(estimate) || math.IsInf(estimate, 0) {
		return nil
	}
	return &estimate
}

// RefreshGroup recomputes one group's dynamic quota state.
func (u *GroupDynamicQuotaUpdater) RefreshGroup(ctx context.Context, groupID int64) error {
	group, err := u.groupRepo.GetByID(ctx, groupID)
	if err != nil {
		return err
	}
	if !group.DynamicQuotaEnabled || group.Platform != PlatformOpenAI || !group.IsSubscriptionType() {
		return u.updateGroup(ctx, groupID, nil)
	}
	accountIDs, err := u.groupRepo.GetAccountIDsByGroupIDs(ctx, []int64{groupID})
	if err != nil {
		return err
	}
	accounts, err := u.accountRepo.GetByIDs(ctx, accountIDs)
	if err != nil {
		return err
	}
	// Sum valid estimates; accounts without a computable estimate contribute nothing.
	sum := 0.0
	for i := range accounts {
		estimate, err := u.accountEstimate(ctx, accounts[i])
		if err != nil {
			return err
		}
		if estimate != nil {
			sum += *estimate
		}
	}
	var estimateSum *float64
	if sum > 0 && !math.IsNaN(sum) && !math.IsInf(sum, 0) {
		estimateSum = &sum
	}
	return u.updateGroup(ctx, groupID, estimateSum)
}

func (u *GroupDynamicQuotaUpdater) AccountChanged(ctx context.Context, accountID int64) {
	if u != nil {
		if err := u.RefreshAccountGroups(ctx, accountID); err != nil {
			slog.Warn("refresh_account_dynamic_quota_failed", "account_id", accountID, "error", err)
		}
	}
}

// RefreshAccountGroups refreshes every dynamic group bound to the account.
// Cross-group abnormality depends on the account's full binding set, so all of
// its dynamic groups are recomputed, not just the one that triggered the change.
func (u *GroupDynamicQuotaUpdater) RefreshAccountGroups(ctx context.Context, accountID int64) error {
	groupIDs, err := u.groupRepo.ListDynamicGroupIDsByAccount(ctx, accountID)
	if err != nil {
		return err
	}
	for _, groupID := range groupIDs {
		if err := u.RefreshGroup(ctx, groupID); err != nil {
			return err
		}
	}
	return nil
}

// RefreshAccountUsage refreshes the account's dynamic groups from a fresh usage snapshot.
func (u *GroupDynamicQuotaUpdater) RefreshAccountUsage(ctx context.Context, account *Account, usage *UsageInfo) error {
	if !account.IsOpenAIChatGPTSubscription() || account.IsShadow() || usage == nil || usage.SevenDay == nil {
		return nil
	}
	return u.RefreshAccountGroups(ctx, account.ID)
}

func (u *GroupDynamicQuotaUpdater) accountEstimate(ctx context.Context, account *Account) (*float64, error) {
	if !account.IsOpenAIChatGPTSubscription() || account.IsShadow() {
		return nil, nil
	}
	now := time.Now()
	window := buildCodexUsageProgressFromExtra(account.Extra, "7d", now)
	if window == nil || window.Utilization <= 0 {
		return nil, nil
	}
	stats, err := u.usageRepo.GetAccountWindowStats(ctx, account.ID, codexWindowStatsStart(window, 7*24*time.Hour, now))
	if err != nil {
		return nil, err
	}
	window.WindowStats = windowStatsFromAccountStats(stats)
	return estimateOpenAIQuota(window), nil
}

func (u *GroupDynamicQuotaUpdater) updateGroup(ctx context.Context, groupID int64, estimateSum *float64) error {
	changed, err := u.groupRepo.UpdateDynamicQuotaState(ctx, groupID, estimateSum)
	if err != nil {
		return err
	}
	if changed && u.invalidator != nil {
		u.invalidator.InvalidateAuthCacheByGroupID(ctx, groupID)
	}
	return nil
}

func (s *adminServiceImpl) refreshGroupDynamicQuota(ctx context.Context, groupID int64) {
	if s.dynamicQuotaUpdater != nil {
		if err := s.dynamicQuotaUpdater.RefreshGroup(ctx, groupID); err != nil {
			slog.Warn("refresh_group_dynamic_quota_failed", "group_id", groupID, "error", err)
		}
	}
}

func (s *adminServiceImpl) refreshAccountDynamicQuota(ctx context.Context, accountID int64) {
	s.dynamicQuotaUpdater.AccountChanged(ctx, accountID)
}

func (s *adminServiceImpl) refreshDynamicQuotaGroups(ctx context.Context, groupIDs ...[]int64) {
	if s.dynamicQuotaUpdater == nil {
		return
	}
	seen := make(map[int64]struct{})
	for _, ids := range groupIDs {
		for _, id := range ids {
			if _, exists := seen[id]; !exists {
				seen[id] = struct{}{}
				s.refreshGroupDynamicQuota(ctx, id)
			}
		}
	}
}

func (s *adminServiceImpl) groupDynamicQuotaAccountIDs(ctx context.Context, groupID int64) ([]int64, error) {
	if s.dynamicQuotaUpdater == nil {
		return nil, nil
	}
	accountIDs, err := s.groupRepo.GetAccountIDsByGroupIDs(ctx, []int64{groupID})
	if err != nil {
		return nil, fmt.Errorf("snapshot group %d account bindings for dynamic quota: %w", groupID, err)
	}
	return accountIDs, nil
}

// Include old bindings after replacement/deletion; their remaining groups may recover.
func (s *adminServiceImpl) refreshGroupAndAccountDynamicQuota(ctx context.Context, groupID int64, accountIDs []int64) {
	if s.dynamicQuotaUpdater == nil {
		return
	}
	var groupIDs []int64
	if groupID > 0 {
		groupIDs = append(groupIDs, groupID)
	}
	seenAccounts := make(map[int64]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if _, exists := seenAccounts[accountID]; exists {
			continue
		}
		seenAccounts[accountID] = struct{}{}
		neighbors, err := s.groupRepo.ListDynamicGroupIDsByAccount(ctx, accountID)
		if err != nil {
			slog.Warn("list_account_dynamic_quota_groups_failed", "account_id", accountID, "error", err)
			continue
		}
		groupIDs = append(groupIDs, neighbors...)
	}
	s.refreshDynamicQuotaGroups(ctx, groupIDs)
}
