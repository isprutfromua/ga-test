package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/isprutfromua/ga-test/internal/models"
)

type mockPGError struct {
	state string
}

func (e mockPGError) Error() string    { return "pg error" }
func (e mockPGError) SQLState() string { return e.state }

func TestCreate(t *testing.T) {
	tests := []struct {
		name      string
		rowErr    error
		wantErrIs error
		wantRaw   bool
	}{
		{name: "success"},
		{name: "maps unique violation", rowErr: mockPGError{state: "23505"}, wantErrIs: ErrAlreadyExists},
		{name: "returns raw error", rowErr: errors.New("db failure"), wantRaw: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock, closeDB := newMockRepo(t)
			defer closeDB()

			sub := &models.Subscription{
				Email:            "u@example.com",
				Repo:             "owner/repo",
				Confirmed:        false,
				LastSeenTag:      "",
				ConfirmToken:     "confirm",
				UnsubscribeToken: "unsub",
			}

			q := regexp.QuoteMeta(`
INSERT INTO subscriptions (email, repo, confirmed, last_seen_tag, confirm_token, unsubscribe_token)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, created_at, updated_at`)
			exp := mock.ExpectQuery(q).WithArgs(sub.Email, sub.Repo, sub.Confirmed, sub.LastSeenTag, sub.ConfirmToken, sub.UnsubscribeToken)

			now := time.Now()
			if tt.rowErr != nil {
				exp.WillReturnError(tt.rowErr)
			} else {
				exp.WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(int64(11), now, now))
			}

			err := repo.Create(context.Background(), sub)
			if tt.wantErrIs != nil {
				if !errors.Is(err, tt.wantErrIs) {
					t.Fatalf("Create() error = %v, want %v", err, tt.wantErrIs)
				}
			} else if tt.wantRaw {
				if err == nil || err.Error() != tt.rowErr.Error() {
					t.Fatalf("Create() error = %v, want raw %v", err, tt.rowErr)
				}
			} else if err != nil {
				t.Fatalf("Create() unexpected error: %v", err)
			}

			if tt.rowErr == nil {
				if sub.ID != 11 {
					t.Fatalf("sub.ID = %d, want 11", sub.ID)
				}
				if sub.CreatedAt.IsZero() || sub.UpdatedAt.IsZero() {
					t.Fatal("expected CreatedAt and UpdatedAt to be populated")
				}
			}

			mustMeetExpectations(t, mock)
		})
	}
}

func TestGetByTokens(t *testing.T) {
	tests := []struct {
		name       string
		byConfirm  bool
		scanErr    error
		wantErrIs  error
		wantSubID  int64
		wantSubRepo string
	}{
		{name: "confirm token success", byConfirm: true, wantSubID: 7, wantSubRepo: "owner/repo"},
		{name: "unsubscribe token success", byConfirm: false, wantSubID: 7, wantSubRepo: "owner/repo"},
		{name: "confirm token not found", byConfirm: true, scanErr: sql.ErrNoRows, wantErrIs: ErrNotFound},
		{name: "unsubscribe token query error", byConfirm: false, scanErr: errors.New("query failed")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock, closeDB := newMockRepo(t)
			defer closeDB()

			clause := "confirm_token = $1"
			if !tt.byConfirm {
				clause = "unsubscribe_token = $1"
			}
			q := regexp.QuoteMeta(`
SELECT id, email, repo, confirmed, last_seen_tag, confirm_token, unsubscribe_token, created_at, updated_at
FROM subscriptions WHERE ` + clause)

			exp := mock.ExpectQuery(q).WithArgs("token")
			now := time.Now()
			if tt.scanErr != nil {
				exp.WillReturnError(tt.scanErr)
			} else {
				exp.WillReturnRows(sqlmock.NewRows([]string{"id", "email", "repo", "confirmed", "last_seen_tag", "confirm_token", "unsubscribe_token", "created_at", "updated_at"}).AddRow(int64(7), "u@example.com", "owner/repo", true, "v1.0.0", "c", "u", now, now))
			}

			var sub *models.Subscription
			var err error
			if tt.byConfirm {
				sub, err = repo.GetByConfirmToken(context.Background(), "token")
			} else {
				sub, err = repo.GetByUnsubscribeToken(context.Background(), "token")
			}

			if tt.wantErrIs != nil {
				if !errors.Is(err, tt.wantErrIs) {
					t.Fatalf("error = %v, want %v", err, tt.wantErrIs)
				}
			} else if tt.scanErr != nil {
				if err == nil || err.Error() != tt.scanErr.Error() {
					t.Fatalf("error = %v, want %v", err, tt.scanErr)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.scanErr == nil {
				if sub == nil {
					t.Fatal("expected non-nil subscription")
				}
				if sub.ID != tt.wantSubID || sub.Repo != tt.wantSubRepo {
					t.Fatalf("subscription = %#v, want ID=%d Repo=%q", sub, tt.wantSubID, tt.wantSubRepo)
				}
			}

			mustMeetExpectations(t, mock)
		})
	}
}

func TestConfirmDeleteAndUpdateLastSeenTag(t *testing.T) {
	type action struct {
		name      string
		call      func(SubscriptionRepository) error
		query     string
		args      []driver.Value
		wantErrIs error
	}

	makeTests := func(method string, call func(SubscriptionRepository) error, query string, args []driver.Value) []action {
		return []action{
			{name: method + " success", call: call, query: query, args: args},
			{name: method + " exec error", call: call, query: query, args: args},
			{name: method + " rows affected error", call: call, query: query, args: args},
			{name: method + " not found", call: call, query: query, args: args, wantErrIs: ErrNotFound},
		}
	}

	tests := []action{}
	tests = append(tests, makeTests(
		"Confirm",
		func(r SubscriptionRepository) error { return r.Confirm(context.Background(), 10) },
		`UPDATE subscriptions SET confirmed = TRUE, updated_at = NOW() WHERE id = $1`,
		[]driver.Value{10},
	)...)
	tests = append(tests, makeTests(
		"Delete",
		func(r SubscriptionRepository) error { return r.Delete(context.Background(), 10) },
		`DELETE FROM subscriptions WHERE id = $1`,
		[]driver.Value{10},
	)...)
	tests = append(tests, makeTests(
		"UpdateLastSeenTag",
		func(r SubscriptionRepository) error { return r.UpdateLastSeenTag(context.Background(), 10, "v2.0.0") },
		`UPDATE subscriptions SET last_seen_tag = $2, updated_at = NOW() WHERE id = $1`,
		[]driver.Value{10, "v2.0.0"},
	)...)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock, closeDB := newMockRepo(t)
			defer closeDB()

			exp := mock.ExpectExec(regexp.QuoteMeta(tt.query)).WithArgs(tt.args...)

			switch {
			case strings.Contains(tt.name, "exec error"):
				exp.WillReturnError(errors.New("exec failed"))
			case strings.Contains(tt.name, "rows affected error"):
				exp.WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected failed")))
			case tt.wantErrIs != nil:
				exp.WillReturnResult(sqlmock.NewResult(0, 0))
			default:
				exp.WillReturnResult(sqlmock.NewResult(0, 1))
			}

			err := tt.call(repo)
			if tt.wantErrIs != nil {
				if !errors.Is(err, tt.wantErrIs) {
					t.Fatalf("error = %v, want %v", err, tt.wantErrIs)
				}
			} else if strings.Contains(tt.name, "exec error") || strings.Contains(tt.name, "rows affected error") {
				if err == nil {
					t.Fatal("expected error")
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			mustMeetExpectations(t, mock)
		})
	}
}
func TestGetByEmailAndGetAllConfirmed(t *testing.T) {
	t.Run("GetByEmail query error", func(t *testing.T) {
		repo, mock, closeDB := newMockRepo(t)
		defer closeDB()

		q := regexp.QuoteMeta(`
SELECT id, email, repo, confirmed, last_seen_tag, confirm_token, unsubscribe_token, created_at, updated_at
FROM subscriptions WHERE email = $1 ORDER BY id ASC`)
		mock.ExpectQuery(q).WithArgs("u@example.com").WillReturnError(errors.New("query failed"))

		subs, err := repo.GetByEmail(context.Background(), "u@example.com")
		if err == nil {
			t.Fatal("expected query error")
		}
		if subs != nil {
			t.Fatalf("subs = %#v, want nil on query error", subs)
		}
		mustMeetExpectations(t, mock)
	})

	t.Run("GetByEmail empty returns non-nil slice", func(t *testing.T) {
		repo, mock, closeDB := newMockRepo(t)
		defer closeDB()

		q := regexp.QuoteMeta(`
SELECT id, email, repo, confirmed, last_seen_tag, confirm_token, unsubscribe_token, created_at, updated_at
FROM subscriptions WHERE email = $1 ORDER BY id ASC`)
		mock.ExpectQuery(q).WithArgs("u@example.com").WillReturnRows(sqlmock.NewRows([]string{"id", "email", "repo", "confirmed", "last_seen_tag", "confirm_token", "unsubscribe_token", "created_at", "updated_at"}))

		subs, err := repo.GetByEmail(context.Background(), "u@example.com")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if subs == nil || len(subs) != 0 {
			t.Fatalf("subs = %#v, want non-nil empty slice", subs)
		}
		mustMeetExpectations(t, mock)
	})

	t.Run("GetAllConfirmed success", func(t *testing.T) {
		repo, mock, closeDB := newMockRepo(t)
		defer closeDB()

		q := regexp.QuoteMeta(`
SELECT id, email, repo, confirmed, last_seen_tag, confirm_token, unsubscribe_token, created_at, updated_at
FROM subscriptions WHERE confirmed = TRUE ORDER BY id ASC`)
		now := time.Now()
		rows := sqlmock.NewRows([]string{"id", "email", "repo", "confirmed", "last_seen_tag", "confirm_token", "unsubscribe_token", "created_at", "updated_at"}).
			AddRow(int64(1), "u1@example.com", "owner/repo1", true, "v1", "c1", "u1", now, now).
			AddRow(int64(2), "u2@example.com", "owner/repo2", true, "v2", "c2", "u2", now, now)
		mock.ExpectQuery(q).WillReturnRows(rows)

		subs, err := repo.GetAllConfirmed(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(subs) != 2 {
			t.Fatalf("len(subs) = %d, want 2", len(subs))
		}
		mustMeetExpectations(t, mock)
	})
}

func TestScanSubscriptions(t *testing.T) {
	t.Run("scan error returns error", func(t *testing.T) {
		repo, mock, closeDB := newMockRepo(t)
		defer closeDB()

		q := regexp.QuoteMeta(`
SELECT id, email, repo, confirmed, last_seen_tag, confirm_token, unsubscribe_token, created_at, updated_at
FROM subscriptions WHERE email = $1 ORDER BY id ASC`)
		rows := sqlmock.NewRows([]string{"id", "email", "repo", "confirmed", "last_seen_tag", "confirm_token", "unsubscribe_token", "created_at", "updated_at"}).
			AddRow("bad-id", "u@example.com", "owner/repo", true, "v1", "c", "u", time.Now(), time.Now())
		mock.ExpectQuery(q).WithArgs("u@example.com").WillReturnRows(rows)

		_, err := repo.GetByEmail(context.Background(), "u@example.com")
		if err == nil {
			t.Fatal("expected scan error")
		}
		mustMeetExpectations(t, mock)
	})

	t.Run("row iteration error returns error", func(t *testing.T) {
		repo, mock, closeDB := newMockRepo(t)
		defer closeDB()

		q := regexp.QuoteMeta(`
SELECT id, email, repo, confirmed, last_seen_tag, confirm_token, unsubscribe_token, created_at, updated_at
FROM subscriptions WHERE email = $1 ORDER BY id ASC`)
		rows := sqlmock.NewRows([]string{"id", "email", "repo", "confirmed", "last_seen_tag", "confirm_token", "unsubscribe_token", "created_at", "updated_at"}).
			AddRow(int64(1), "u@example.com", "owner/repo", true, "v1", "c", "u", time.Now(), time.Now())
		rows.RowError(0, errors.New("row error"))
		mock.ExpectQuery(q).WithArgs("u@example.com").WillReturnRows(rows)

		_, err := repo.GetByEmail(context.Background(), "u@example.com")
		if err == nil {
			t.Fatal("expected row iteration error")
		}
		mustMeetExpectations(t, mock)
	})
}

func TestIsUniqueViolation(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "non pg error", err: errors.New("x"), want: false},
		{name: "other sqlstate", err: mockPGError{state: "40001"}, want: false},
		{name: "unique violation", err: mockPGError{state: "23505"}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isUniqueViolation(tt.err); got != tt.want {
				t.Fatalf("isUniqueViolation(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func newMockRepo(t *testing.T) (SubscriptionRepository, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error: %v", err)
	}
	return NewPostgresRepository(db), mock, func() { _ = db.Close() }
}

func mustMeetExpectations(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations not met: %v", err)
	}
}
