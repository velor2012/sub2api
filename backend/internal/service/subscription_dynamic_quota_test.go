package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type dynamicQuotaSubscriptionRepo struct {
	UserSubscriptionRepository
	sub        UserSubscription
	countCalls int
	countErr   error
}

func (r *dynamicQuotaSubscriptionRepo) GetByID(context.Context, int64) (*UserSubscription, error) {
	sub := r.sub
	return &sub, nil
}

func (r *dynamicQuotaSubscriptionRepo) GetActiveByUserIDAndGroupID(context.Context, int64, int64) (*UserSubscription, error) {
	sub := r.sub
	return &sub, nil
}

func (r *dynamicQuotaSubscriptionRepo) ListByUserID(context.Context, int64) ([]UserSubscription, error) {
	return []UserSubscription{r.sub}, nil
}

func (r *dynamicQuotaSubscriptionRepo) ListActiveByUserID(ctx context.Context, userID int64) ([]UserSubscription, error) {
	return r.ListByUserID(ctx, userID)
}

func (r *dynamicQuotaSubscriptionRepo) CountActiveByGroupID(context.Context, int64) (int64, error) {
	r.countCalls++
	return 0, r.countErr
}

func TestSubscriptionDynamicQuotaConsumers(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name         string
		enabled      bool
		daily        float64
		weekly       float64
		estimate     *float64
		wantDaily    float64
		wantWeekly   float64
		wantDailyOK  bool
		wantWeeklyOK bool
	}{
		{"disabled preserves fixed", false, 20, 30, ptrFloat64(5), 20, 30, true, true},
		{"dynamic caps both", true, 20, 30, ptrFloat64(5), 5, 5, false, false},
		{"daily zero remains unlimited", true, 0, 0, ptrFloat64(5), 0, 5, true, false},
		{"missing estimate uses fixed", true, 20, 30, nil, 20, 30, true, true},
		{"fixed lower than dynamic", true, 6, 7, ptrFloat64(50), 6, 7, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := &Group{
				ID: 1, Platform: PlatformOpenAI, SubscriptionType: SubscriptionTypeSubscription,
				DailyLimitUSD: ptrFloat64(tc.daily), WeeklyLimitUSD: ptrFloat64(tc.weekly),
				DynamicQuotaEnabled: tc.enabled, DynamicQuotaLimitUSD: tc.estimate,
			}
			sub := UserSubscription{
				ID: 1, UserID: 1, GroupID: 1, Group: group, Status: SubscriptionStatusActive,
				StartsAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour),
				DailyWindowStart: &now, WeeklyWindowStart: &now, MonthlyWindowStart: &now,
				DailyUsageUSD: 8, WeeklyUsageUSD: 8,
			}
			repo := &dynamicQuotaSubscriptionRepo{sub: sub}
			svc := &SubscriptionService{userSubRepo: repo, now: func() time.Time { return now }}
			billing := &BillingCacheService{subRepo: repo}

			require.Equal(t, tc.wantDailyOK, sub.CheckDailyLimit(group, 0))
			require.Equal(t, tc.wantWeeklyOK, sub.CheckWeeklyLimit(group, 0))
			var wantErr error
			if !tc.wantDailyOK {
				wantErr = ErrDailyLimitExceeded
			} else if !tc.wantWeeklyOK {
				wantErr = ErrWeeklyLimitExceeded
			}
			_, err := svc.ValidateAndCheckLimits(&sub, group)
			require.Equal(t, wantErr, err)
			require.Equal(t, wantErr, billing.checkSubscriptionEligibility(context.Background(), 1, group, &sub))
			require.Equal(t, wantErr, billing.checkFixedChargeSubscription(context.Background(), 1, group, 0))

			progress := svc.calculateProgress(&sub, group)
			for _, window := range []struct {
				progress *UsageWindowProgress
				limit    float64
			}{{progress.Daily, tc.wantDaily}, {progress.Weekly, tc.wantWeekly}} {
				if window.limit == 0 {
					require.Nil(t, window.progress)
					continue
				}
				require.NotNil(t, window.progress)
				require.Equal(t, window.limit, window.progress.LimitUSD)
				require.Equal(t, 8.0, window.progress.UsedUSD)
				if window.limit < 8 {
					require.Equal(t, 100.0, window.progress.Percentage)
					require.Zero(t, window.progress.RemainingUSD)
				}
			}
			require.Zero(t, repo.countCalls, "quota checks must not count subscribers")
		})
	}
}

func TestSubscriptionListIgnoresDynamicSeatCounts(t *testing.T) {
	group := &Group{
		ID: 1, Platform: PlatformOpenAI, SubscriptionType: SubscriptionTypeSubscription,
		DynamicQuotaEnabled: true, DynamicQuotaLimitUSD: ptrFloat64(5),
	}
	repo := &dynamicQuotaSubscriptionRepo{sub: UserSubscription{GroupID: 1, Group: group}}
	svc := &SubscriptionService{userSubRepo: repo}
	_, err := svc.ListActiveUserSubscriptions(context.Background(), 1)
	require.NoError(t, err)
	_, err = svc.ListUserSubscriptions(context.Background(), 1)
	require.NoError(t, err, "ordinary subscriptions must not depend on dynamic seat counts")
}
