package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/isprutfromua/ga-test/internal/models"
	"github.com/isprutfromua/ga-test/internal/tokenhash"
)

const dbOperationTimeout = 5 * time.Second

var (
	ErrAlreadyExists = errors.New("already exists")
	ErrNotFound      = errors.New("not found")
)

type SubscriptionRepository interface {
	Create(ctx context.Context, sub *models.Subscription) error
	GetByConfirmToken(ctx context.Context, token string) (*models.Subscription, error)
	GetByUnsubscribeToken(ctx context.Context, token string) (*models.Subscription, error)
	AcquireScanLock(ctx context.Context) (release func(), acquired bool, err error)
	WasNotified(ctx context.Context, subscriptionID int64, tag string) (bool, error)
	MarkNotified(ctx context.Context, subscriptionID int64, tag string) error
	Confirm(ctx context.Context, id int64) error
	Delete(ctx context.Context, id int64) error
	GetByEmail(ctx context.Context, email string) ([]*models.Subscription, error)
	GetAllConfirmed(ctx context.Context) ([]*models.Subscription, error)
	UpdateLastSeenTag(ctx context.Context, id int64, tag string) error
}

type postgresRepository struct{ db *sql.DB }

const scannerAdvisoryLockKey int64 = 20426001

func NewPostgresRepository(db *sql.DB) SubscriptionRepository { return &postgresRepository{db: db} }

func (r *postgresRepository) Create(ctx context.Context, sub *models.Subscription) error {
	ctx, cancel := context.WithTimeout(ctx, dbOperationTimeout)
	defer cancel()

	const q = `
INSERT INTO subscriptions (email, repo, confirmed, last_seen_tag, confirm_token, unsubscribe_token)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, created_at, updated_at`
	if err := r.db.QueryRowContext(ctx, q, sub.Email, sub.Repo, sub.Confirmed, sub.LastSeenTag, sub.ConfirmToken, sub.UnsubscribeToken).Scan(&sub.ID, &sub.CreatedAt, &sub.UpdatedAt); err != nil {
		if isUniqueViolation(err) { return ErrAlreadyExists }
		return err
	}
	return nil
}

func (r *postgresRepository) GetByConfirmToken(ctx context.Context, token string) (*models.Subscription, error) {
	ctx, cancel := context.WithTimeout(ctx, dbOperationTimeout)
	defer cancel()
	hash := tokenhash.Hash(token)

	const q = `
SELECT id, email, repo, confirmed, last_seen_tag, confirm_token, unsubscribe_token, created_at, updated_at
FROM subscriptions WHERE confirm_token = $1 OR confirm_token = $2`

	return r.getByQuery(ctx, q, token, hash)
}

func (r *postgresRepository) GetByUnsubscribeToken(ctx context.Context, token string) (*models.Subscription, error) {
	ctx, cancel := context.WithTimeout(ctx, dbOperationTimeout)
	defer cancel()
	hash := tokenhash.Hash(token)

	const q = `
SELECT id, email, repo, confirmed, last_seen_tag, confirm_token, unsubscribe_token, created_at, updated_at
FROM subscriptions WHERE unsubscribe_token = $1 OR unsubscribe_token = $2`

	return r.getByQuery(ctx, q, token, hash)
}

func (r *postgresRepository) AcquireScanLock(ctx context.Context) (release func(), acquired bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, dbOperationTimeout)
	defer cancel()

	conn, err := r.db.Conn(ctx)
	if err != nil {
		return nil, false, err
	}

	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, scannerAdvisoryLockKey).Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, false, err
	}
	if !acquired {
		_ = conn.Close()
		return nil, false, nil
	}

	release = func() {
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), dbOperationTimeout)
		defer unlockCancel()
		_, _ = conn.ExecContext(unlockCtx, `SELECT pg_advisory_unlock($1)`, scannerAdvisoryLockKey)
		_ = conn.Close()
	}
	return release, true, nil
}

func (r *postgresRepository) WasNotified(ctx context.Context, subscriptionID int64, tag string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, dbOperationTimeout)
	defer cancel()

	const q = `
SELECT EXISTS(
	SELECT 1 FROM notification_log WHERE subscription_id = $1 AND tag_name = $2
)`

	var exists bool
	if err := r.db.QueryRowContext(ctx, q, subscriptionID, tag).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (r *postgresRepository) MarkNotified(ctx context.Context, subscriptionID int64, tag string) error {
	ctx, cancel := context.WithTimeout(ctx, dbOperationTimeout)
	defer cancel()

	_, err := r.db.ExecContext(ctx, `
INSERT INTO notification_log (subscription_id, tag_name)
VALUES ($1, $2)
ON CONFLICT (subscription_id, tag_name) DO NOTHING`, subscriptionID, tag)
	return err
}

func (r *postgresRepository) Confirm(ctx context.Context, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, dbOperationTimeout)
	defer cancel()

	res, err := r.db.ExecContext(ctx, `UPDATE subscriptions SET confirmed = TRUE, updated_at = NOW() WHERE id = $1`, id)
	if err != nil { return err }
	rows, err := res.RowsAffected()
	if err != nil { return err }
	if rows == 0 { return ErrNotFound }
	return nil
}

func (r *postgresRepository) Delete(ctx context.Context, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, dbOperationTimeout)
	defer cancel()

	res, err := r.db.ExecContext(ctx, `DELETE FROM subscriptions WHERE id = $1`, id)
	if err != nil { return err }
	rows, err := res.RowsAffected()
	if err != nil { return err }
	if rows == 0 { return ErrNotFound }
	return nil
}

func (r *postgresRepository) GetByEmail(ctx context.Context, email string) ([]*models.Subscription, error) {
	ctx, cancel := context.WithTimeout(ctx, dbOperationTimeout)
	defer cancel()

	rows, err := r.db.QueryContext(ctx, `
SELECT id, email, repo, confirmed, last_seen_tag, confirm_token, unsubscribe_token, created_at, updated_at
FROM subscriptions WHERE email = $1 ORDER BY id ASC`, email)
	if err != nil { return nil, err }
	defer rows.Close()
	return scanSubscriptions(rows)
}

func (r *postgresRepository) GetAllConfirmed(ctx context.Context) ([]*models.Subscription, error) {
	ctx, cancel := context.WithTimeout(ctx, dbOperationTimeout)
	defer cancel()

	rows, err := r.db.QueryContext(ctx, `
SELECT id, email, repo, confirmed, last_seen_tag, confirm_token, unsubscribe_token, created_at, updated_at
FROM subscriptions WHERE confirmed = TRUE ORDER BY id ASC`)
	if err != nil { return nil, err }
	defer rows.Close()
	return scanSubscriptions(rows)
}

func (r *postgresRepository) UpdateLastSeenTag(ctx context.Context, id int64, tag string) error {
	ctx, cancel := context.WithTimeout(ctx, dbOperationTimeout)
	defer cancel()

	res, err := r.db.ExecContext(ctx, `UPDATE subscriptions SET last_seen_tag = $2, updated_at = NOW() WHERE id = $1`, id, tag)
	if err != nil { return err }
	rows, err := res.RowsAffected()
	if err != nil { return err }
	if rows == 0 { return ErrNotFound }
	return nil
}

func (r *postgresRepository) getByQuery(ctx context.Context, query string, args ...any) (*models.Subscription, error) {
	row := r.db.QueryRowContext(ctx, query, args...)
	sub := &models.Subscription{}
	if err := row.Scan(&sub.ID, &sub.Email, &sub.Repo, &sub.Confirmed, &sub.LastSeenTag, &sub.ConfirmToken, &sub.UnsubscribeToken, &sub.CreatedAt, &sub.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return nil, ErrNotFound }
		return nil, err
	}
	return sub, nil
}


func scanSubscriptions(rows *sql.Rows) ([]*models.Subscription, error) {
	var subs []*models.Subscription
	for rows.Next() {
		sub := &models.Subscription{}
		if err := rows.Scan(&sub.ID, &sub.Email, &sub.Repo, &sub.Confirmed, &sub.LastSeenTag, &sub.ConfirmToken, &sub.UnsubscribeToken, &sub.CreatedAt, &sub.UpdatedAt); err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	if err := rows.Err(); err != nil { return nil, err }
	if subs == nil { subs = []*models.Subscription{} }
	return subs, nil
}

func isUniqueViolation(err error) bool {
	type pgErr interface{ SQLState() string }
	var pe pgErr
	return errors.As(err, &pe) && pe.SQLState() == "23505"
}
