package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestContactControlsReceiptPauseAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controls.db")
	now := time.Now()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	enableContactForTest(t, s)
	_, err = s.CreateContact(ctx, contactFixture(1, now))
	check(err)
	check(s.SetContactControl(ctx, "notifications", false))
	_, err = s.CreateContact(ctx, contactFixture(2, now))
	check(err)
	if _, err = s.ClaimContact(ctx, now, 20, 300); !errors.Is(err, ErrContactPaused) {
		t.Fatal(err)
	}
	m, err := s.Contact(ctx, 2)
	check(err)
	if m.NotificationState != "cancelled" || m.NotificationCode != "notifications_disabled" {
		t.Fatal(m)
	}
	check(s.SetContactControl(ctx, "form", false))
	if _, err = s.CreateContact(ctx, contactFixture(3, now)); !errors.Is(err, ErrContactClosed) {
		t.Fatal(err)
	}
	if created, err := s.CreateContact(ctx, contactFixture(1, now)); created || err != nil {
		t.Fatal("lost accepted retry", created, err)
	}
	check(s.Close())
	s, err = Open(ctx, path)
	check(err)
	settings, err := s.ContactSettings(ctx)
	check(err)
	if settings.FormEnabled || settings.NotificationsEnabled {
		t.Fatal("settings reset")
	}
	check(s.SetContactControl(ctx, "notifications", true))
	m, err = s.ClaimContact(ctx, now, 20, 300)
	check(err)
	if m.ID != 1 {
		t.Fatal("did not resume prior pending notification")
	}
	check(s.FinishContactNotification(ctx, m.ID, "accepted", "accepted", "", now))
	if _, err = s.ClaimContact(ctx, now, 20, 300); !errors.Is(err, ErrNotFound) {
		t.Fatal("sent inbox-only message", err)
	}
	check(s.SetContactControl(ctx, "form", true))
	_, err = s.CreateContact(ctx, contactFixture(3, now))
	check(err)
	if err = s.SetContactControl(ctx, "notifications_enabled=1 --", true); err == nil {
		t.Fatal("accepted unknown control")
	}
}
func TestContactTestQueueControlsAndBudget(t *testing.T) {
	for _, control := range []string{"form", "notifications"} {
		t.Run(control, func(t *testing.T) {
			ctx := context.Background()
			s := contactDatabase(t)
			now := time.Now()
			id, err := s.QueueContactTest(ctx, fmt.Sprintf("%064d", 1), now, 1, 1)
			if err != nil {
				t.Fatal(err)
			}
			again, err := s.QueueContactTest(ctx, fmt.Sprintf("%064d", 1), now, 1, 1)
			if err != nil || id != again {
				t.Fatal("duplicate test", err)
			}
			if _, err = s.QueueContactTest(ctx, fmt.Sprintf("%064d", 2), now, 1, 1); !errors.Is(err, ErrContactTestPending) {
				t.Fatal(err)
			}
			if err = s.SetContactControl(ctx, control, false); err != nil {
				t.Fatal(err)
			}
			m, err := s.Contact(ctx, id)
			if err != nil || !m.IsTest || m.NotificationState != "cancelled" {
				t.Fatal(m, err)
			}
			if _, err = s.QueueContactTest(ctx, fmt.Sprintf("%064d", 2), now, 1, 1); err == nil {
				t.Fatal("queued disabled test")
			}
			if err = s.SetContactControl(ctx, control, true); err != nil {
				t.Fatal(err)
			}
			if _, err = s.QueueContactTest(ctx, fmt.Sprintf("%064d", 2), now, 1, 1); err != nil {
				t.Fatal(err)
			}
			m, err = s.ClaimContact(ctx, now, 1, 1)
			if err != nil || !m.IsTest || m.Email != "contact@munichbrief.de" {
				t.Fatal(m, err)
			}
			if err = s.SetContactControl(ctx, control, false); err != nil {
				t.Fatal(err)
			}
			if err = s.FinishContactNotification(ctx, m.ID, "uncertain", "transport", "", now); err != nil {
				t.Fatal(err)
			}
			if err = s.UpdateContact(ctx, m.ID, "retry", "", 0, now.Unix()); err == nil {
				t.Fatal("retried disabled test")
			}
			if err = s.SetContactControl(ctx, control, true); err != nil {
				t.Fatal(err)
			}
			if _, err = s.QueueContactTest(ctx, fmt.Sprintf("%064d", 3), now, 1, 1); !errors.Is(err, ErrContactBudget) {
				t.Fatal(err)
			}
			budgets, err := s.ContactBudgets(ctx, now, 1, 1)
			if err != nil {
				t.Fatal(err)
			}
			for _, b := range budgets {
				if b.Used != 1 || b.Remaining != 0 {
					t.Fatal(b)
				}
			}
		})
	}
}
func TestContactBudgetUTCResetAndLoweredLimit(t *testing.T) {
	ctx := context.Background()
	s := contactDatabase(t)
	now := time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC)
	for i := 1; i <= 2; i++ {
		if _, err := s.CreateContact(ctx, contactFixture(i, now)); err != nil {
			t.Fatal(err)
		}
		m, err := s.ClaimContact(ctx, now, 20, 300)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.FinishContactNotification(ctx, m.ID, "failed", "rejected", "", now); err != nil {
			t.Fatal(err)
		}
	}
	budgets, err := s.ContactBudgets(ctx, now.In(time.FixedZone("other", 7200)), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range budgets {
		if b.Used != 2 || b.Remaining != 0 || !b.ResetAt.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
			t.Fatal(b)
		}
	}
	budgets, err = s.ContactBudgets(ctx, now.Add(time.Minute), 20, 300)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range budgets {
		if b.Used != 0 || b.Remaining != b.Limit {
			t.Fatal(b)
		}
	}
}

func TestContactLaunchClosedMigrationAndSavedControls(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "launch.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	settings, err := s.ContactSettings(ctx)
	if err != nil || settings.FormEnabled || settings.NotificationsEnabled {
		t.Fatalf("new database must start closed: %+v, %v", settings, err)
	}
	now := time.Now()
	if _, err := s.CreateContact(ctx, contactFixture(1, now)); !errors.Is(err, ErrContactClosed) {
		t.Fatalf("closed installation accepted correspondence: %v", err)
	}
	enableContactForTest(t, s)
	if _, err := s.CreateContact(ctx, contactFixture(1, now)); err != nil {
		t.Fatal(err)
	}
	testID, err := s.QueueContactTest(ctx, fmt.Sprintf("%064d", 2), now, 20, 300)
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the pre-026 state without modifying any historical migration.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=26`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	settings, err = s.ContactSettings(ctx)
	if err != nil || settings.FormEnabled || settings.NotificationsEnabled {
		t.Fatalf("upgrade opened contact: %+v %v", settings, err)
	}
	if m, err := s.Contact(ctx, 1); err != nil || m.Message != contactFixture(1, now).Message || m.NotificationState != "pending" {
		t.Fatalf("upgrade lost correspondence/queue: %+v %v", m, err)
	}
	if m, err := s.Contact(ctx, testID); err != nil || m.NotificationCode != "test_disabled" {
		t.Fatalf("queued test survived closure: %+v %v", m, err)
	}
	if _, err := s.ClaimContact(ctx, now, 20, 300); !errors.Is(err, ErrContactPaused) {
		t.Fatalf("upgrade allowed sending: %v", err)
	}
	enableContactForTest(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	settings, err = s.ContactSettings(ctx)
	if err != nil || !settings.FormEnabled || !settings.NotificationsEnabled {
		t.Fatalf("restart reset saved controls: %+v %v", settings, err)
	}
}
