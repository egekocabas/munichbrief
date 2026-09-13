package store

import (
	"context"
	"errors"
	"time"
)

var (
	ErrContactClosed      = errors.New("contact form closed")
	ErrContactPaused      = errors.New("contact notifications disabled")
	ErrContactTestPending = errors.New("a test notification is already queued or sending")
)

type ContactSettings struct{ FormEnabled, NotificationsEnabled bool }

func (s *Store) ContactSettings(ctx context.Context) (ContactSettings, error) {
	var v ContactSettings
	err := s.db.QueryRowContext(ctx, `SELECT form_enabled,notifications_enabled FROM contact_settings WHERE id=1`).Scan(&v.FormEnabled, &v.NotificationsEnabled)
	return v, err
}

// Each control changes independently. Cancelling queued tests prevents a test
// disabled before dispatch from unexpectedly sending when controls are reopened.
// Work already claimed by the sender may finish after this transaction commits.
func (s *Store) SetContactControl(ctx context.Context, control string, enabled bool) error {
	column := ""
	switch control {
	case "form":
		column = "form_enabled"
	case "notifications":
		column = "notifications_enabled"
	default:
		return errors.New("invalid contact control")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE contact_settings SET `+column+`=? WHERE id=1`, enabled); err != nil {
		return err
	}
	if !enabled {
		if _, err = tx.ExecContext(ctx, `UPDATE contact_messages SET notification_state='cancelled',notification_code='test_disabled' WHERE is_test=1 AND notification_state IN ('pending','retry')`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type ContactBudget struct {
	Period, Label          string
	Limit, Used, Remaining int
	ResetAt                time.Time
}

func contactBudgets(now time.Time, daily, monthly int) []ContactBudget {
	now = now.UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return []ContactBudget{{Period: now.Format("2006-01-02"), Label: "Today", Limit: daily, ResetAt: day.AddDate(0, 0, 1)}, {Period: now.Format("2006-01"), Label: "This month", Limit: monthly, ResetAt: month.AddDate(0, 1, 0)}}
}
func (s *Store) ContactBudgets(ctx context.Context, now time.Time, daily, monthly int) ([]ContactBudget, error) {
	budgets := contactBudgets(now, daily, monthly)
	for i := range budgets {
		b := &budgets[i]
		if err := s.db.QueryRowContext(ctx, `SELECT coalesce((SELECT attempts FROM contact_budgets WHERE period=?),0)`, b.Period).Scan(&b.Used); err != nil {
			return nil, err
		}
		b.Remaining = max(0, b.Limit-b.Used)
	}
	return budgets, nil
}

// QueueContactTest uses the same durable queue and budgets as real notifications.
// Caller supplies only a signed action's hash: the envelope and contents are fixed.
func (s *Store) QueueContactTest(ctx context.Context, hash string, now time.Time, daily, monthly int) (int64, error) {
	m := ContactMessage{SubmissionHash: hash, Email: "contact@munichbrief.de", Topic: "general", Language: "en", CreatedAt: now.Unix(), Message: "This is a test notification requested from MunichBrief administration. No visitor submitted this message. If you received this email in your Gmail mailbox, the email delivery chain reached that mailbox."}
	if err := m.Validate(); err != nil {
		return 0, err
	}
	if _, err := s.createContact(ctx, m, true, now, daily, monthly); err != nil {
		return 0, err
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM contact_messages WHERE submission_hash=?`, hash).Scan(&id)
	return id, err
}
