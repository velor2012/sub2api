package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestDynamicSubscriptionRemainingUsesEffectiveLimits(t *testing.T) {
	fixedDaily, fixedWeekly, derived, zero := 20.0, 80.0, 10.0, 0.0
	group := &service.Group{
		Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeSubscription,
		DailyLimitUSD: &fixedDaily, WeeklyLimitUSD: &fixedWeekly,
		DynamicQuotaEnabled: true, DynamicQuotaLimitUSD: &derived,
	}
	sub := &service.UserSubscription{DailyUsageUSD: 8, WeeklyUsageUSD: 7}
	handler := &GatewayHandler{}
	require.Equal(t, 2.0, handler.calculateSubscriptionRemaining(group, sub))

	group.DailyLimitUSD, group.WeeklyLimitUSD = &zero, &zero
	sub.DailyUsageUSD = 100
	require.Equal(t, 3.0, handler.calculateSubscriptionRemaining(group, sub), "unlimited day must not hide the dynamic weekly cap")
	sub.WeeklyUsageUSD = 11
	require.Zero(t, handler.calculateSubscriptionRemaining(group, sub), "reduced quota can be below real usage")

	group.DynamicQuotaLimitUSD = nil
	require.Equal(t, -1.0, handler.calculateSubscriptionRemaining(group, sub), "missing dynamic quota must restore fixed unlimited behavior")
}
