package service

import (
	"math"
	"testing"
)

func TestGroupDynamicQuotaEffectiveLimits(t *testing.T) {
	fixed, derived := 10.0, 4.0
	g := &Group{Platform: PlatformOpenAI, SubscriptionType: SubscriptionTypeSubscription, DynamicQuotaEnabled: true, DynamicQuotaLimitUSD: &derived}
	if !g.UsesDynamicQuota() || g.EffectiveDailyLimitUSD() != nil || g.DynamicDailyLimitUSD() != 0 || *g.EffectiveWeeklyLimitUSD() != 4 {
		t.Fatal("unlimited daily must remain unlimited; weekly uses dynamic quota")
	}
	zero := 0.0
	g.DailyLimitUSD = &zero
	if g.HasDailyLimit() || g.EffectiveDailyLimitUSD() != &zero {
		t.Fatal("fixed daily zero must remain unlimited")
	}
	g.DailyLimitUSD, g.WeeklyLimitUSD = &fixed, &fixed
	if *g.EffectiveDailyLimitUSD() != 4 || *g.EffectiveWeeklyLimitUSD() != 4 || g.DynamicDailyLimitUSD() != 4 {
		t.Fatal("dynamic quota must cap positive fixed limits")
	}
	derived = 20
	if g.EffectiveDailyLimitUSD() != &fixed || g.EffectiveWeeklyLimitUSD() != &fixed {
		t.Fatal("fixed caps must remain authoritative")
	}
	derived = math.Inf(1)
	if g.UsesDynamicQuota() || g.EffectiveWeeklyLimitUSD() != &fixed {
		t.Fatal("invalid estimates must fall back to fixed caps")
	}
	g.DynamicQuotaEnabled = false
	derived = 4
	if g.UsesDynamicQuota() || g.EffectiveDailyLimitUSD() != &fixed {
		t.Fatal("disabled dynamic quota must preserve fixed caps")
	}
}
