package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sukhera/ping/db"
)

// fakeQuerier implements db.Querier by embedding it (nil) and overriding only
// the methods a given test needs; calling an un-overridden method panics via
// the nil embedded interface, which surfaces as an immediate test failure.
type fakeQuerier struct {
	db.Querier

	createUserFn                func(ctx context.Context, arg db.CreateUserParams) (db.User, error)
	getUserByEmailFn            func(ctx context.Context, email string) (db.User, error)
	getRefreshTokenByHashFn     func(ctx context.Context, tokenHash string) (db.RefreshToken, error)
	createRefreshTokenFn        func(ctx context.Context, arg db.CreateRefreshTokenParams) (db.RefreshToken, error)
	rotateRefreshTokenIfUnrotFn func(ctx context.Context, id pgtype.UUID) (db.RefreshToken, error)
	revokeRefreshTokenFamFn     func(ctx context.Context, familyID pgtype.UUID) error

	createMonitorFn        func(ctx context.Context, arg db.CreateMonitorParams) (db.Monitor, error)
	getMonitorByIDFn       func(ctx context.Context, id pgtype.UUID) (db.Monitor, error)
	listMonitorsByUserPgFn func(ctx context.Context, arg db.ListMonitorsByUserPageParams) ([]db.Monitor, error)
	updateMonitorFn        func(ctx context.Context, arg db.UpdateMonitorParams) (db.Monitor, error)
	deleteMonitorFn        func(ctx context.Context, arg db.DeleteMonitorParams) (int64, error)
}

func (f *fakeQuerier) CreateUser(ctx context.Context, arg db.CreateUserParams) (db.User, error) {
	return f.createUserFn(ctx, arg)
}

func (f *fakeQuerier) GetUserByEmail(ctx context.Context, email string) (db.User, error) {
	return f.getUserByEmailFn(ctx, email)
}

func (f *fakeQuerier) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (db.RefreshToken, error) {
	return f.getRefreshTokenByHashFn(ctx, tokenHash)
}

func (f *fakeQuerier) CreateRefreshToken(ctx context.Context, arg db.CreateRefreshTokenParams) (db.RefreshToken, error) {
	return f.createRefreshTokenFn(ctx, arg)
}

func (f *fakeQuerier) RotateRefreshTokenIfUnrotated(ctx context.Context, id pgtype.UUID) (db.RefreshToken, error) {
	return f.rotateRefreshTokenIfUnrotFn(ctx, id)
}

func (f *fakeQuerier) RevokeRefreshTokenFamily(ctx context.Context, familyID pgtype.UUID) error {
	return f.revokeRefreshTokenFamFn(ctx, familyID)
}

func (f *fakeQuerier) CreateMonitor(ctx context.Context, arg db.CreateMonitorParams) (db.Monitor, error) {
	return f.createMonitorFn(ctx, arg)
}

func (f *fakeQuerier) GetMonitorByID(ctx context.Context, id pgtype.UUID) (db.Monitor, error) {
	return f.getMonitorByIDFn(ctx, id)
}

func (f *fakeQuerier) ListMonitorsByUserPage(ctx context.Context, arg db.ListMonitorsByUserPageParams) ([]db.Monitor, error) {
	return f.listMonitorsByUserPgFn(ctx, arg)
}

func (f *fakeQuerier) UpdateMonitor(ctx context.Context, arg db.UpdateMonitorParams) (db.Monitor, error) {
	return f.updateMonitorFn(ctx, arg)
}

func (f *fakeQuerier) DeleteMonitor(ctx context.Context, arg db.DeleteMonitorParams) (int64, error) {
	return f.deleteMonitorFn(ctx, arg)
}

func newTestStore(q db.Querier) *Store {
	return &Store{q: q}
}
