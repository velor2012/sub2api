//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func quotaReconcileGroup(s *GroupRepoSuite, name string, positions *int64) *dbent.Group {
	s.T().Helper()
	group, err := s.tx.Client().Group.Create().
		SetName(name).
		SetPlatform(service.PlatformOpenAI).
		SetSubscriptionType(service.SubscriptionTypeSubscription).
		SetDynamicQuotaEnabled(true).
		SetNillableDynamicQuotaPositions(positions).
		Save(s.ctx)
	s.Require().NoError(err)
	return group
}

func (s *GroupRepoSuite) TestListDynamicQuotaGroupsWithPositionMismatch_CountsBeyondFirstThousand() {
	persisted := int64(1000)
	crowded := quotaReconcileGroup(s, "quota-reconcile-crowded", &persisted)
	zero := int64(0)
	late := quotaReconcileGroup(s, "quota-reconcile-late", &zero)
	users := make([]*dbent.UserCreate, 1001)
	for i := range users {
		users[i] = s.tx.Client().User.Create().
			SetEmail(fmt.Sprintf("quota-reconcile-%d@example.com", i)).
			SetPasswordHash("test-password-hash")
	}
	created, err := s.tx.Client().User.CreateBulk(users...).Save(s.ctx)
	s.Require().NoError(err)
	subs := make([]*dbent.UserSubscriptionCreate, len(created))
	for i, user := range created {
		subs[i] = s.tx.Client().UserSubscription.Create().
			SetUserID(user.ID).SetGroupID(crowded.ID).
			SetStartsAt(time.Now().Add(-time.Hour)).SetExpiresAt(time.Now().Add(time.Hour)).
			SetStatus(service.SubscriptionStatusActive)
	}
	_, err = s.tx.Client().UserSubscription.CreateBulk(subs...).Save(s.ctx)
	s.Require().NoError(err)
	mustCreateSubscription(s.T(), s.tx.Client(), &service.UserSubscription{UserID: created[0].ID, GroupID: late.ID})

	ids, err := s.repo.ListDynamicQuotaGroupsWithPositionMismatch(s.ctx)
	s.Require().NoError(err)
	s.Require().Equal([]int64{crowded.ID, late.ID}, ids)

	for _, id := range ids {
		_, err := s.repo.UpdateDynamicQuotaState(s.ctx, id, nil)
		s.Require().NoError(err)
	}
	stored, err := s.repo.GetByID(s.ctx, crowded.ID)
	s.Require().NoError(err)
	s.Require().NotNil(stored.DynamicQuotaPositions)
	s.Require().Equal(int64(1001), *stored.DynamicQuotaPositions)
	ids, err = s.repo.ListDynamicQuotaGroupsWithPositionMismatch(s.ctx)
	s.Require().NoError(err)
	s.Require().Empty(ids, "reconciled groups must not be selected again")
}

func (s *GroupRepoSuite) TestListDynamicQuotaGroupsWithPositionMismatch_EmptyDeletedAndConsistentGroups() {
	one, zero := int64(1), int64(0)
	empty := quotaReconcileGroup(s, "quota-reconcile-empty", &one)
	unset := quotaReconcileGroup(s, "quota-reconcile-unset", nil)
	quotaReconcileGroup(s, "quota-reconcile-consistent-empty", &zero)
	filtered := quotaReconcileGroup(s, "quota-reconcile-filtered", &one)
	consistent := quotaReconcileGroup(s, "quota-reconcile-consistent", &one)
	user := mustCreateUser(s.T(), s.tx.Client(), &service.User{Email: "quota-reconcile-live@example.com"})
	deletedUser := mustCreateUser(s.T(), s.tx.Client(), &service.User{Email: "quota-reconcile-deleted@example.com"})
	_, err := s.tx.ExecContext(s.ctx, "UPDATE users SET deleted_at=now() WHERE id=$1", deletedUser.ID)
	s.Require().NoError(err)
	mustCreateSubscription(s.T(), s.tx.Client(), &service.UserSubscription{UserID: deletedUser.ID, GroupID: filtered.ID})
	mustCreateSubscription(s.T(), s.tx.Client(), &service.UserSubscription{UserID: user.ID, GroupID: consistent.ID})

	for i, status := range []string{service.SubscriptionStatusActive, service.SubscriptionStatusExpired, service.SubscriptionStatusActive} {
		u := mustCreateUser(s.T(), s.tx.Client(), &service.User{Email: fmt.Sprintf("quota-reconcile-excluded-%d@example.com", i)})
		sub := mustCreateSubscription(s.T(), s.tx.Client(), &service.UserSubscription{UserID: u.ID, GroupID: filtered.ID, Status: status})
		if i == 0 {
			_, err = s.tx.ExecContext(s.ctx, "UPDATE user_subscriptions SET expires_at=now()-interval '1 hour' WHERE id=$1", sub.ID)
		} else if i == 2 {
			_, err = s.tx.ExecContext(s.ctx, "UPDATE user_subscriptions SET deleted_at=now() WHERE id=$1", sub.ID)
		}
		s.Require().NoError(err)
	}
	for i, change := range []string{"deleted_at=now()", "platform='anthropic'", "subscription_type='standard'", "dynamic_quota_enabled=false"} {
		group := quotaReconcileGroup(s, fmt.Sprintf("quota-reconcile-ineligible-%d", i), &one)
		_, err := s.tx.ExecContext(s.ctx, "UPDATE groups SET "+change+" WHERE id=$1", group.ID)
		s.Require().NoError(err)
	}

	ids, err := s.repo.ListDynamicQuotaGroupsWithPositionMismatch(s.ctx)
	s.Require().NoError(err)
	s.Require().Equal([]int64{empty.ID, unset.ID, filtered.ID}, ids)
	for _, id := range ids {
		_, err := s.repo.UpdateDynamicQuotaState(s.ctx, id, nil)
		s.Require().NoError(err)
		stored, err := s.repo.GetByID(s.ctx, id)
		s.Require().NoError(err)
		s.Require().NotNil(stored.DynamicQuotaPositions)
		s.Require().Zero(*stored.DynamicQuotaPositions)
	}
	ids, err = s.repo.ListDynamicQuotaGroupsWithPositionMismatch(s.ctx)
	s.Require().NoError(err)
	s.Require().Empty(ids)
}

type quotaReconcileFailingGroupRepo struct {
	service.GroupRepository
}

func (quotaReconcileFailingGroupRepo) ListDynamicQuotaGroupsWithPositionMismatch(context.Context) ([]int64, error) {
	return nil, errors.New("quota reconciliation unavailable")
}

func (s *GroupRepoSuite) TestSubscriptionExpiry_ReconcilesPersistedQuotaState() {
	for _, queryFails := range []bool{false, true} {
		s.Run(fmt.Sprintf("query-failure-%t", queryFails), func() {
			one, three := int64(1), int64(3)
			expired := quotaReconcileGroup(s, fmt.Sprintf("quota-expired-%t", queryFails), &one)
			dynamic := quotaReconcileGroup(s, fmt.Sprintf("quota-mismatch-%t", queryFails), &three)
			consistent := quotaReconcileGroup(s, fmt.Sprintf("quota-consistent-%t", queryFails), &one)
			for _, group := range []*dbent.Group{expired, dynamic, consistent} {
				_, err := s.tx.ExecContext(s.ctx, "UPDATE groups SET dynamic_quota_limit_usd=12 WHERE id=$1", group.ID)
				s.Require().NoError(err)
			}
			// Natural expiry must refresh even a group excluded from reconciliation.
			_, err := s.tx.ExecContext(s.ctx, "UPDATE groups SET dynamic_quota_enabled=false WHERE id=$1", expired.ID)
			s.Require().NoError(err)
			user := mustCreateUser(s.T(), s.tx.Client(), &service.User{Email: fmt.Sprintf("quota-expiry-%t@example.com", queryFails)})
			expiredSub := mustCreateSubscription(s.T(), s.tx.Client(), &service.UserSubscription{
				UserID: user.ID, GroupID: expired.ID, ExpiresAt: time.Now().Add(-time.Hour),
			})
			mustCreateSubscription(s.T(), s.tx.Client(), &service.UserSubscription{UserID: user.ID, GroupID: dynamic.ID})
			mustCreateSubscription(s.T(), s.tx.Client(), &service.UserSubscription{UserID: user.ID, GroupID: consistent.ID})
			subRepo := NewUserSubscriptionRepository(s.tx.Client())
			var groups service.GroupRepository = s.repo
			if queryFails {
				groups = quotaReconcileFailingGroupRepo{GroupRepository: s.repo}
			}
			updater := service.NewGroupDynamicQuotaUpdater(groups, newAccountRepositoryWithSQL(s.tx.Client(), s.tx, nil), nil, nil)
			expiry := service.NewSubscriptionExpiryService(subRepo, time.Hour)
			expiry.SetDynamicQuotaUpdater(updater)

			// Start runs its first cycle before Stop can join the worker.
			expiry.Start()
			expiry.Stop()

			storedSub, err := s.tx.Client().UserSubscription.Get(s.ctx, expiredSub.ID)
			s.Require().NoError(err)
			s.Require().Equal(service.SubscriptionStatusExpired, storedSub.Status)
			stored, err := s.repo.GetByID(s.ctx, expired.ID)
			s.Require().NoError(err)
			s.Require().NotNil(stored.DynamicQuotaPositions)
			s.Require().Zero(*stored.DynamicQuotaPositions)
			s.Require().Nil(stored.DynamicQuotaLimitUSD)
			stored, err = s.repo.GetByID(s.ctx, dynamic.ID)
			s.Require().NoError(err)
			s.Require().NotNil(stored.DynamicQuotaPositions)
			if queryFails {
				s.Require().Equal(three, *stored.DynamicQuotaPositions)
				s.Require().NotNil(stored.DynamicQuotaLimitUSD)
			} else {
				s.Require().Equal(one, *stored.DynamicQuotaPositions)
				s.Require().Nil(stored.DynamicQuotaLimitUSD)
			}
			stored, err = s.repo.GetByID(s.ctx, consistent.ID)
			s.Require().NoError(err)
			s.Require().NotNil(stored.DynamicQuotaLimitUSD, "consistent groups must not be recomputed without a trigger")
			s.Require().Equal(12.0, *stored.DynamicQuotaLimitUSD)
		})
	}
}
