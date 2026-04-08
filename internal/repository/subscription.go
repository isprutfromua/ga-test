package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/isprutfromua/ga-test/internal/models"
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
	Confirm(ctx context.Context, id int64) error
	Delete(ctx context.Context, id int64) error
	GetByEmail(ctx context.Context, email string) ([]*models.Subscription, error)
	GetAllConfirmed(ctx context.Context) ([]*models.Subscription, error)
	UpdateLastSeenTag(ctx context.Context, id int64, tag string) error
}

type postgresRepository struct{ db *sql.DB }

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

	const q = `
SELECT id, email, repo, confirmed, last_seen_tag, confirm_token, unsubscribe_token, created_at, updated_at
FROM subscriptions WHERE confirm_token = $1`

	return r.getByQuery(ctx, q, token)
}

func (r *postgresRepository) GetByUnsubscribeToken(ctx context.Context, token string) (*models.Subscription, error) {
	ctx, cancel := context.WithTimeout(ctx, dbOperationTimeout)
	defer cancel()

	const q = `
SELECT id, email, repo, confirmed, last_seen_tag, confirm_token, unsubscribe_token, created_at, updated_at
FROM subscriptions WHERE unsubscribe_token = $1`

	return r.getByQuery(ctx, q, token)
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

func (r *postgresRepository) getByQuery(ctx context.Context, query string, arg any) (*models.Subscription, error) {
	row := r.db.QueryRowContext(ctx, query, arg)
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
