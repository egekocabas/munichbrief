package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func contactDatabase(t *testing.T) *Store {
	t.Helper()
	s, e := Open(context.Background(), filepath.Join(t.TempDir(), "contact.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func contactFixture(n int, now time.Time) ContactMessage {
	return ContactMessage{SubmissionHash: fmt.Sprintf("%064d", n), Email: "reader@example.org", Topic: "correction", Message: "请更正这个信息。 İstanbul <script>alert(1)</script>", Language: "zh", CreatedAt: now.Unix()}
}
func TestContactReceiptOrderingAndBudget(t *testing.T) {
	ctx := context.Background()
	s := contactDatabase(t)
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	for n := 1; n <= 25; n++ {
		if ok, e := s.CreateContact(ctx, contactFixture(n, now)); !ok || e != nil {
			t.Fatal(ok, e)
		}
	}
	if ok, e := s.CreateContact(ctx, contactFixture(1, now)); ok || e != nil {
		t.Fatal("duplicate", ok, e)
	}
	rows, total, e := s.ListContacts(ctx, "", 1)
	if e != nil || total != 25 || len(rows) != 20 || rows[0].ID != 25 {
		t.Fatal(total, len(rows), e)
	}
	rows, _, _ = s.ListContacts(ctx, "", 2)
	if len(rows) != 5 || rows[0].ID != 5 {
		t.Fatal(rows)
	}
	m, e := s.ClaimContact(ctx, now, 1, 2)
	if e != nil || m.ID != 1 {
		t.Fatal(m, e)
	}
	if e = s.UpdateContact(ctx, m.ID, "delete", "", 0, now.Unix()); e == nil {
		t.Fatal("deleted in-flight message")
	}
	if e = s.FinishContactNotification(ctx, m.ID, "accepted", "accepted", "id", now); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClaimContact(ctx, now, 1, 2); !errors.Is(e, ErrContactBudget) {
		t.Fatal(e)
	}
	m, e = s.ClaimContact(ctx, now.Add(24*time.Hour), 1, 2)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RecoverContactNotifications(ctx); e != nil {
		t.Fatal(e)
	}
	m, _ = s.Contact(ctx, m.ID)
	if m.NotificationState != "uncertain" {
		t.Fatal(m.NotificationState)
	}
	if _, e = s.ClaimContact(ctx, now.Add(48*time.Hour), 1, 2); !errors.Is(e, ErrContactBudget) {
		t.Fatal(e)
	}
	if _, e = s.ClaimContact(ctx, now.AddDate(0, 1, 0), 1, 2); e != nil {
		t.Fatal(e)
	}
}
func TestContactRetentionReopenAndHolds(t *testing.T) {
	ctx := context.Background()
	s := contactDatabase(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for n := 1; n <= 4; n++ {
		_, e := s.CreateContact(ctx, contactFixture(n, now))
		if e != nil {
			t.Fatal(e)
		}
	}
	for _, id := range []int64{1, 2, 3} {
		if e := s.UpdateContact(ctx, id, "resolve", "", 0, now.Unix()); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.UpdateContact(ctx, 2, "reopen", "", 0, now.Add(time.Hour).Unix()); e != nil {
		t.Fatal(e)
	}
	if e := s.UpdateContact(ctx, 3, "hold", "Ongoing correction dispute", now.Add(24*time.Hour).Unix(), now.Unix()); e != nil {
		t.Fatal(e)
	}
	if e := s.PurgeContacts(ctx, now.Add(ContactRetention-time.Second)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Contact(ctx, 1); e != nil {
		t.Fatal("early deletion", e)
	}
	if e := s.PurgeContacts(ctx, now.Add(ContactRetention)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Contact(ctx, 1); !errors.Is(e, ErrNotFound) {
		t.Fatal("not purged", e)
	}
	for _, id := range []int64{2, 3, 4} {
		if _, e := s.Contact(ctx, id); e != nil {
			t.Fatal(id, e)
		}
	}
	if e := s.UpdateContact(ctx, 3, "release", "", 0, now.Add(ContactRetention).Unix()); e != nil {
		t.Fatal(e)
	}
	if e := s.PurgeContacts(ctx, now.Add(ContactRetention)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Contact(ctx, 3); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	m, _ := s.Contact(ctx, 2)
	if m.ResolvedAt != 0 {
		t.Fatal("reopen did not cancel expiry")
	}
}
func TestContactUnicodeValidationAndDatabaseFailure(t *testing.T) {
	ctx := context.Background()
	s := contactDatabase(t)
	m := contactFixture(1, time.Now())
	m.Message = strings.Repeat("好", 5000)
	if _, e := s.CreateContact(ctx, m); e != nil {
		t.Fatal(e)
	}
	m.Message += "好"
	if m.Validate() == nil {
		t.Fatal("accepted 5001 code points")
	}
	m = contactFixture(2, time.Now())
	m.Email = "a@example.org\r\nBcc: victim@example.org"
	if m.Validate() == nil {
		t.Fatal("header injection")
	}
	s.Close()
	if _, e := s.CreateContact(ctx, contactFixture(3, time.Now())); e == nil {
		t.Fatal("acknowledged closed database")
	}
}
func BenchmarkContactInbox10000(b *testing.B) {
	s, e := Open(context.Background(), filepath.Join(b.TempDir(), "benchmark.db"))
	if e != nil {
		b.Fatal(e)
	}
	defer s.Close()
	tx, e := s.db.Begin()
	if e != nil {
		b.Fatal(e)
	}
	for n := 0; n < 10000; n++ {
		_, e = tx.Exec(`INSERT INTO contact_messages(submission_hash,email,topic,message,language,created_at) VALUES(?,?,?,?,?,?)`, fmt.Sprintf("%064d", n), "synthetic@example.org", "general", "Synthetic enquiry", "en", n+1)
		if e != nil {
			b.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		b.Fatal(e)
	}
	b.ResetTimer()
	for range b.N {
		if _, _, e := s.ListContacts(context.Background(), "", 250); e != nil {
			b.Fatal(e)
		}
	}
}

func TestContactRestartDurabilityAndStableReferences(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "restart.db")
	now := time.Now().UTC()
	s, e := Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateContact(ctx, contactFixture(1, now)); e != nil {
		t.Fatal(e)
	}
	m, e := s.ClaimContact(ctx, now, 1, 1)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.RecoverContactNotifications(ctx); e != nil {
		t.Fatal(e)
	}
	saved, e := s.Contact(ctx, m.ID)
	if e != nil || saved.NotificationState != "uncertain" || saved.Message != m.Message {
		t.Fatal("lost interrupted submission", e)
	}
	if _, e = s.ClaimContact(ctx, now, 1, 1); !errors.Is(e, ErrContactBudget) {
		t.Fatal("lost budget", e)
	}
	if e = s.UpdateContact(ctx, m.ID, "delete", "", 0, now.Unix()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateContact(ctx, contactFixture(2, now)); e != nil {
		t.Fatal(e)
	}
	rows, _, e := s.ListContacts(ctx, "", 1)
	if e != nil || len(rows) != 1 || rows[0].ID <= m.ID {
		t.Fatal("reused internal reference", e)
	}
}

func TestContactReadAndResolveAgain(t *testing.T) {
	ctx := context.Background()
	s := contactDatabase(t)
	now := time.Now()
	_, e := s.CreateContact(ctx, contactFixture(1, now))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.UpdateContact(ctx, 1, "read", "", 0, now.Unix()); e != nil {
		t.Fatal(e)
	}
	m, _ := s.Contact(ctx, 1)
	if m.ResolvedAt != 0 {
		t.Fatal("reading started expiry")
	}
	for _, action := range []string{"resolve", "reopen"} {
		if e = s.UpdateContact(ctx, 1, action, "", 0, now.Unix()); e != nil {
			t.Fatal(e)
		}
	}
	later := now.Add(48 * time.Hour)
	if e = s.UpdateContact(ctx, 1, "resolve", "", 0, later.Unix()); e != nil {
		t.Fatal(e)
	}
	if e = s.PurgeContacts(ctx, now.Add(ContactRetention)); e != nil {
		t.Fatal(e)
	}
	m, e = s.Contact(ctx, 1)
	if e != nil || m.ResolvedAt != later.Unix() || m.NotificationState != "cancelled" {
		t.Fatal("new resolution deadline lost", e)
	}
}
