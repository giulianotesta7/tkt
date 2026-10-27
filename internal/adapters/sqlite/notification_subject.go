package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
)

// notificationSubjectStore implements application.NotificationSubjectStore
// (issue #419, S3/S12): the factual read the notification center renders the
// account kind from.
//
// It is deliberately NOT the Pending tab's read. PendingUserStore.ListPending
// answers who waits RIGHT NOW, which is what approval needs; a stored notice
// outlives that state, so this read answers the account the notice names
// whatever its state is today (waiting, approved, deactivated) and lets the
// application layer decide what the row may say. Nothing here filters on the
// pending flag, and nothing here reads the issuer, the subject or the address
// last seen at the provider: those are the identity's own record, not a
// notification fact.
type notificationSubjectStore struct {
	db *sql.DB
}

var _ application.NotificationSubjectStore = (*notificationSubjectStore)(nil)

func newNotificationSubjectStore(db *sql.DB) *notificationSubjectStore {
	return &notificationSubjectStore{db: db}
}

// NotificationSubjectStore returns the account-subject read the bell and the
// full notification feed render from (issue #419, S3/S12).
func (s *Store) NotificationSubjectStore() application.NotificationSubjectStore {
	return newNotificationSubjectStore(s.db)
}

// ListNotificationSubjects answers the stored accounts behind the requested
// notice subjects, plus the provider identity each one holds. The account read
// is the account's own record (name and address as stored on the account), and
// the provider read joins the identities WITHOUT the pending filter the Pending
// tab applies, so an approved account still renders the method it signed in
// with.
//
// Rows come back in the order the caller asked for, one per existing account.
// An id with no account is absent (a notice whose account was deleted is
// rendered as such by the caller), and an account with no stored identity
// carries no provider rather than a guessed one.
func (ns *notificationSubjectStore) ListNotificationSubjects(ctx context.Context, userIDs []int64) ([]application.NotificationAccountSubject, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	args := make([]any, len(userIDs))
	for i, id := range userIDs {
		args[i] = id
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(userIDs)), ",")

	rows, err := ns.db.QueryContext(ctx,
		`SELECT id, name, email, pending FROM users WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list notification subjects: %w", err)
	}
	defer rows.Close()
	found := make(map[int64]application.NotificationAccountSubject, len(userIDs))
	for rows.Next() {
		var (
			subject application.NotificationAccountSubject
			pending int64
		)
		if err := rows.Scan(&subject.UserID, &subject.Name, &subject.Email, &pending); err != nil {
			return nil, fmt.Errorf("sqlite: scan notification subject: %w", err)
		}
		subject.Pending = pending == 1
		found[subject.UserID] = subject
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list notification subjects: %w", err)
	}
	if len(found) == 0 {
		return nil, nil
	}

	providers, err := ns.listProviders(ctx, placeholders, args)
	if err != nil {
		return nil, err
	}
	out := make([]application.NotificationAccountSubject, 0, len(found))
	for _, id := range userIDs {
		subject, ok := found[id]
		if !ok {
			continue
		}
		subject.Providers = providers[id]
		out = append(out, subject)
		delete(found, id)
	}
	return out, nil
}

// listProviders answers the provider identities held by the same accounts, one
// row per stored identity, ordered by account and provider so the read is
// deterministic without a display decision (the application layer owns that).
func (ns *notificationSubjectStore) listProviders(ctx context.Context, placeholders string, args []any) (map[int64][]application.CompanyProvider, error) {
	rows, err := ns.db.QueryContext(ctx,
		`SELECT user_id, provider FROM user_identities WHERE user_id IN (`+placeholders+`) ORDER BY user_id ASC, provider ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list notification subject providers: %w", err)
	}
	defer rows.Close()
	providers := make(map[int64][]application.CompanyProvider)
	for rows.Next() {
		var (
			userID   int64
			provider string
		)
		if err := rows.Scan(&userID, &provider); err != nil {
			return nil, fmt.Errorf("sqlite: scan notification subject provider: %w", err)
		}
		providers[userID] = append(providers[userID], application.CompanyProvider(provider))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list notification subject providers: %w", err)
	}
	return providers, nil
}
