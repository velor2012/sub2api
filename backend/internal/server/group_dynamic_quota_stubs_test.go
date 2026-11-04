//go:build unit

package server_test

import "context"

func (stubGroupRepo) UpdateDynamicQuotaState(context.Context, int64, *float64) (bool, error) {
	return false, nil
}
func (stubGroupRepo) ListDynamicGroupIDsByAccount(context.Context, int64) ([]int64, error) {
	return nil, nil
}
func (stubGroupRepo) ListDynamicQuotaGroupsWithPositionMismatch(context.Context) ([]int64, error) {
	return nil, nil
}
