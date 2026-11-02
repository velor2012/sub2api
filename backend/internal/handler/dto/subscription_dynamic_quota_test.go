package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUserSubscriptionDynamicQuotaLimits(t *testing.T) {
	fixedDaily, fixedWeekly, derived := 20.0, 0.0, 5.123456
	positions := int64(2)
	group := &service.Group{
		Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeSubscription,
		DailyLimitUSD: &fixedDaily, WeeklyLimitUSD: &fixedWeekly,
		DynamicQuotaEnabled: true, DynamicQuotaPositions: &positions, DynamicQuotaLimitUSD: &derived,
	}
	sub := &service.UserSubscription{Group: group, DailyUsageUSD: 25}
	out := UserSubscriptionFromService(sub)
	require.NotNil(t, out.QuotaLimits)
	require.True(t, out.QuotaLimits.DynamicEnabled)
	require.False(t, out.QuotaLimits.Abnormal)
	require.Equal(t, &positions, out.QuotaLimits.Positions)
	require.Equal(t, derived, out.QuotaLimits.DynamicDailyLimitUSD)
	require.Equal(t, derived, out.QuotaLimits.DynamicWeeklyLimitUSD)
	require.Equal(t, derived, *out.QuotaLimits.EffectiveDailyLimitUSD)
	require.Equal(t, derived, *out.QuotaLimits.EffectiveWeeklyLimitUSD)
	require.Equal(t, 25.0, out.DailyUsageUSD)
	adminGroup := GroupFromServiceAdmin(group)
	require.Equal(t, fixedDaily, *adminGroup.DailyLimitUSD)
	require.Equal(t, fixedWeekly, *adminGroup.WeeklyLimitUSD)
	require.Equal(t, derived, *adminGroup.DynamicQuotaLimitUSD)
	require.True(t, adminGroup.DynamicQuotaEnabled)
	require.Equal(t, &positions, adminGroup.DynamicQuotaPositions)
	require.False(t, adminGroup.DynamicQuotaAbnormal)

	group.DailyLimitUSD = nil
	out = UserSubscriptionFromService(sub)
	require.Zero(t, out.QuotaLimits.DynamicDailyLimitUSD)
	require.Nil(t, out.QuotaLimits.EffectiveDailyLimitUSD)
	encoded, err := json.Marshal(out)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(encoded, &payload))
	require.Contains(t, payload["quota_limits"], "effective_daily_limit_usd")

	// Abnormal groups still return quota_limits (fallback values) with the abnormal flag.
	group.DynamicQuotaAbnormal = true
	out = UserSubscriptionFromService(sub)
	require.NotNil(t, out.QuotaLimits)
	require.True(t, out.QuotaLimits.Abnormal)
	require.Zero(t, out.QuotaLimits.DynamicDailyLimitUSD)
	require.Zero(t, out.QuotaLimits.DynamicWeeklyLimitUSD)
	require.NotNil(t, out.QuotaLimits.EffectiveWeeklyLimitUSD)
	require.Zero(t, *out.QuotaLimits.EffectiveWeeklyLimitUSD, "abnormal falls back to fixed weekly (0 = unlimited)")
	group.DynamicQuotaAbnormal = false

	group.DynamicQuotaLimitUSD = nil
	out = UserSubscriptionFromService(sub)
	require.NotNil(t, out.QuotaLimits, "dynamic_enabled groups always return quota_limits")
	require.Zero(t, out.QuotaLimits.DynamicWeeklyLimitUSD)
	encoded, err = json.Marshal(out)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "quota_limits")
}
