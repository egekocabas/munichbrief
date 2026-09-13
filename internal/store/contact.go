package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"
)

const ContactRetention = 90 * 24 * time.Hour

type ContactMessage struct {
	ID                                              int64
	IsTest                                          bool
	SubmissionHash, Email, Topic, Message, Language string
	CreatedAt, ReadAt, ResolvedAt                   int64
	HoldReason                                      string
	HoldReviewAt                                    int64
	NotificationState                               string
	NotificationAttempts                            int
	NextAttemptAt                                   int64
	NotificationCode, ProviderID                    string
}

func (m ContactMessage) Validate() error {
	a, err := mail.ParseAddress(m.Email)
	if err != nil || a.Address != m.Email || len(m.Email) > 254 || strings.ContainsAny(m.Email, "\r\n") {
		return errors.New("email")
	}
	if m.Topic != "general" && m.Topic != "privacy" && m.Topic != "correction" {
		return errors.New("topic")
	}
	if !utf8.ValidString(m.Message) || strings.TrimSpace(m.Message) == "" || utf8.RuneCountInString(m.Message) > 5000 || strings.ContainsRune(m.Message, 0) {
		return errors.New("message")
	}
	if len(m.SubmissionHash) != 64 || len(m.Language) > 20 || m.CreatedAt <= 0 {
		return errors.New("submission")
	}
	return nil
}

// CreateContact atomically stores both correspondence and pending delivery state.
// A repeated form token acknowledges the original submission without replacing it.
func (s *Store) CreateContact(ctx context.Context, m ContactMessage) (bool, error) {
	if err := m.Validate(); err != nil {
		return false, err
	}
	return s.createContact(ctx, m, false, time.Time{}, 0, 0)
}

// The duplicate check and settings check share the insert transaction. A retry
// of an accepted submission stays successful even after the form is closed.
func (s *Store) createContact(ctx context.Context, m ContactMessage, test bool, now time.Time, daily, monthly int) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM contact_messages WHERE submission_hash=?)`, m.SubmissionHash).Scan(&exists); err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	var settings ContactSettings
	if err = tx.QueryRowContext(ctx, `SELECT form_enabled,notifications_enabled FROM contact_settings WHERE id=1`).Scan(&settings.FormEnabled, &settings.NotificationsEnabled); err != nil {
		return false, err
	}
	if !settings.FormEnabled {
		return false, ErrContactClosed
	}
	state, code := "pending", ""
	if !settings.NotificationsEnabled {
		if test {
			return false, ErrContactPaused
		}
		state, code = "cancelled", "notifications_disabled"
	}
	if test {
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM contact_messages WHERE is_test=1 AND notification_state IN ('pending','retry','sending'))`).Scan(&exists); err != nil {
			return false, err
		}
		if exists {
			return false, ErrContactTestPending
		}
		for _, b := range contactBudgets(now, daily, monthly) {
			var used int
			if err = tx.QueryRowContext(ctx, `SELECT coalesce((SELECT attempts FROM contact_budgets WHERE period=?),0)`, b.Period).Scan(&used); err != nil {
				return false, err
			}
			if used >= b.Limit {
				return false, ErrContactBudget
			}
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO contact_messages(submission_hash,email,topic,message,language,created_at,notification_state,notification_code,is_test) VALUES(?,?,?,?,?,?,?,?,?)`, m.SubmissionHash, m.Email, m.Topic, m.Message, m.Language, m.CreatedAt, state, code, test)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

const contactColumns = `id,submission_hash,email,topic,message,language,created_at,read_at,resolved_at,hold_reason,hold_review_at,notification_state,notification_attempts,next_attempt_at,notification_code,provider_id,is_test`

func scanContact(row interface{ Scan(...any) error }) (ContactMessage, error) {
	var m ContactMessage
	err := row.Scan(&m.ID, &m.SubmissionHash, &m.Email, &m.Topic, &m.Message, &m.Language, &m.CreatedAt, &m.ReadAt, &m.ResolvedAt, &m.HoldReason, &m.HoldReviewAt, &m.NotificationState, &m.NotificationAttempts, &m.NextAttemptAt, &m.NotificationCode, &m.ProviderID, &m.IsTest)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return m, err
}

func (s *Store) Contact(ctx context.Context, id int64) (ContactMessage, error) {
	return scanContact(s.db.QueryRowContext(ctx, `SELECT `+contactColumns+` FROM contact_messages WHERE id=?`, id))
}

func (s *Store) ListContacts(ctx context.Context, filter string, page int) ([]ContactMessage, int, error) {
	where := "1=1"
	switch filter {
	case "":
	case "unread":
		where = "read_at=0"
	case "unresolved":
		where = "resolved_at=0"
	case "problems":
		where = "notification_state IN ('retry','failed','uncertain')"
	default:
		return nil, 0, errors.New("invalid filter")
	}
	if page < 1 || page > 1000000 {
		return nil, 0, errors.New("invalid page")
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM contact_messages WHERE `+where).Scan(&count); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+contactColumns+` FROM contact_messages WHERE `+where+` ORDER BY created_at DESC,id DESC LIMIT 20 OFFSET ?`, (page-1)*20)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	messages := []ContactMessage{}
	for rows.Next() {
		m, err := scanContact(rows)
		if err != nil {
			return nil, 0, err
		}
		messages = append(messages, m)
	}
	return messages, count, rows.Err()
}

// UpdateContact refuses destructive changes while a notification is in flight.
func (s *Store) UpdateContact(ctx context.Context, id int64, action, reason string, review, now int64) error {
	var query string
	var args []any
	switch action {
	case "read":
		query = "read_at=?"
		args = []any{now}
	case "unread":
		query = "read_at=0"
	case "resolve":
		query = "resolved_at=CASE WHEN resolved_at=0 THEN ? ELSE resolved_at END,notification_state=CASE WHEN notification_state IN ('pending','retry','failed','uncertain') THEN 'cancelled' ELSE notification_state END"
		args = []any{now}
	case "reopen":
		query = "resolved_at=0"
	case "retry":
		query = "notification_state='pending',next_attempt_at=0,notification_code=''"
	case "hold":
		reason = strings.TrimSpace(reason)
		if reason == "" || utf8.RuneCountInString(reason) > 500 || review <= now || review > now+int64(ContactRetention/time.Second) {
			return errors.New("hold requires a reason and a review date within 90 days")
		}
		query = "hold_reason=?,hold_review_at=?"
		args = []any{reason, review}
	case "release":
		query = "hold_reason='',hold_review_at=0"
	case "delete":
		result, err := s.db.ExecContext(ctx, `DELETE FROM contact_messages WHERE id=? AND notification_state!='sending'`, id)
		return contactChanged(result, err)
	default:
		return errors.New("invalid action")
	}
	args = append(args, id)
	condition := ""
	if action == "retry" {
		condition = " AND resolved_at=0 AND notification_state IN ('failed','uncertain','retry') AND (SELECT notifications_enabled FROM contact_settings WHERE id=1)=1 AND (is_test=0 OR (SELECT form_enabled FROM contact_settings WHERE id=1)=1)"
	}
	result, err := s.db.ExecContext(ctx, `UPDATE contact_messages SET `+query+` WHERE id=? AND notification_state!='sending'`+condition, args...)
	return contactChanged(result, err)
}
func contactChanged(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return errors.New("message unavailable or notification in progress")
	}
	return err
}

func (s *Store) RecoverContactNotifications(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE contact_messages SET notification_state='uncertain',notification_code='process_interrupted' WHERE notification_state='sending'`)
	return err
}

func (s *Store) PurgeContacts(ctx context.Context, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM contact_messages WHERE resolved_at>0 AND resolved_at<=? AND hold_reason='' AND notification_state!='sending'`, now.Add(-ContactRetention).Unix()); err != nil {
		return err
	}
	// Budget rows carry only aggregate counters, never correspondence.
	if _, err = tx.ExecContext(ctx, `DELETE FROM contact_budgets WHERE period < ?`, now.AddDate(0, -2, 0).UTC().Format("2006-01")); err != nil {
		return err
	}
	return tx.Commit()
}

// ClaimContact reserves quota before network activity. Reservations count even
// when acceptance is uncertain, so retries cannot silently exceed the budget.
func (s *Store) ClaimContact(ctx context.Context, now time.Time, daily, monthly int) (ContactMessage, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ContactMessage{}, err
	}
	defer tx.Rollback()
	var settings ContactSettings
	if err = tx.QueryRowContext(ctx, `SELECT form_enabled,notifications_enabled FROM contact_settings WHERE id=1`).Scan(&settings.FormEnabled, &settings.NotificationsEnabled); err != nil {
		return ContactMessage{}, err
	}
	if !settings.NotificationsEnabled {
		return ContactMessage{}, ErrContactPaused
	}
	for _, budget := range contactBudgets(now, daily, monthly) {
		if _, err = tx.ExecContext(ctx, `INSERT INTO contact_budgets(period) VALUES(?) ON CONFLICT DO NOTHING`, budget.Period); err != nil {
			return ContactMessage{}, err
		}
		var used int
		if err = tx.QueryRowContext(ctx, `SELECT attempts FROM contact_budgets WHERE period=?`, budget.Period).Scan(&used); err != nil {
			return ContactMessage{}, err
		}
		if used >= budget.Limit {
			return ContactMessage{}, ErrContactBudget
		}
	}

	m, err := scanContact(tx.QueryRowContext(ctx, `SELECT `+contactColumns+` FROM contact_messages WHERE notification_state IN ('pending','retry') AND resolved_at=0 AND next_attempt_at<=? AND (is_test=0 OR ?=1) ORDER BY id LIMIT 1`, now.Unix(), settings.FormEnabled))
	if err != nil {
		return m, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE contact_messages SET notification_state='sending',notification_attempts=notification_attempts+1 WHERE id=?`, m.ID); err != nil {
		return m, err
	}
	for _, p := range []string{now.UTC().Format("2006-01-02"), now.UTC().Format("2006-01")} {
		if _, err = tx.ExecContext(ctx, `UPDATE contact_budgets SET attempts=attempts+1 WHERE period=?`, p); err != nil {
			return m, err
		}
	}
	m.NotificationAttempts++
	return m, tx.Commit()
}

var ErrContactBudget = errors.New("contact notification budget exhausted")

func (s *Store) FinishContactNotification(ctx context.Context, id int64, state, code, providerID string, next time.Time) error {
	switch state {
	case "accepted", "failed", "retry", "uncertain":
	default:
		return fmt.Errorf("invalid notification result")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE contact_messages SET notification_state=?,notification_code=?,provider_id=?,next_attempt_at=? WHERE id=? AND notification_state='sending'`, state, code, providerID, next.Unix(), id)
	return err
}

type ContactStats struct {
	Total, Pending, Problems int
	Oldest                   int64
}

func (s *Store) ContactStats(ctx context.Context) (ContactStats, error) {
	var v ContactStats
	err := s.db.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(notification_state IN ('pending','retry')),0),coalesce(sum(notification_state IN ('failed','retry','uncertain')),0),coalesce(min(CASE WHEN notification_state IN ('pending','retry','failed','uncertain') AND resolved_at=0 THEN created_at END),0) FROM contact_messages`).Scan(&v.Total, &v.Pending, &v.Problems, &v.Oldest)
	return v, err
}
