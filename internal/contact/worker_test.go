package contact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSMTP2GOResponsesAndFixedEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		body, state string
	}{{"accepted", 200, `{"data":{"succeeded":1,"failed":0,"email_id":"123"}}`, "accepted"}, {"payload failure", 200, `{"data":{"succeeded":0,"failed":1}}`, "failed"}, {"key", 401, `{}`, "failed"}, {"quota", 200, `{"data":{"error_code":"API_KEY_RATELIMIT"}}`, "retry"}, {"limit", 429, `{}`, "retry"}, {"server", 503, `{}`, "retry"}, {"malformed", 200, `broken`, "uncertain"}} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != Endpoint || r.Header.Get("X-Smtp2go-Api-Key") != "test-key" {
					t.Fatal("endpoint/auth")
				}
				var body map[string]any
				if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
					t.Fatal(e)
				}
				if body["sender"] != Address || body["to"].([]any)[0] != Address {
					t.Fatal("not fixed destination")
				}
				if body["custom_headers"].([]any)[0].(map[string]any)["value"] != "reader@example.org" {
					t.Fatal("reply-to")
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Retry-After": []string{"180"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			result := (SMTP2GO{APIKey: "test-key", Client: client}).Send(context.Background(), store.ContactMessage{Email: "reader@example.org", Message: "Hello", Topic: "general"})
			if result.State != tc.state {
				t.Fatal(result)
			}
		})
	}
	result := (SMTP2GO{Client: &http.Client{Transport: transportFunc(func(_ *http.Request) (*http.Response, error) { return nil, errors.New("ambiguous") })}}).Send(context.Background(), store.ContactMessage{})
	if result.State != "uncertain" {
		t.Fatal(result)
	}
}

type fakeSender struct {
	calls  int
	result Result
}

func (s *fakeSender) Send(_ context.Context, _ store.ContactMessage) Result {
	s.calls++
	return s.result
}
func TestWorkerBudgetAndNotificationFailurePreserveInbox(t *testing.T) {
	ctx := context.Background()
	db, e := store.Open(ctx, filepath.Join(t.TempDir(), "contact.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	now := time.Now()
	for n := 1; n <= 3; n++ {
		_, e = db.CreateContact(ctx, store.ContactMessage{SubmissionHash: fmt.Sprintf("%064d", n), Email: "reader@example.org", Topic: "general", Message: "Synthetic", Language: "en", CreatedAt: now.Unix()})
		if e != nil {
			t.Fatal(e)
		}
	}
	sender := &fakeSender{result: Result{State: "uncertain", Code: "transport"}}
	w := Worker{Store: db, Sender: sender, Daily: 1, Monthly: 1, Metrics: &Metrics{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	w.Step(ctx, now)
	w.Step(ctx, now.Add(time.Minute))
	if sender.calls != 1 || !w.Metrics.Paused.Load() {
		t.Fatal(sender.calls)
	}
	rows, count, e := db.ListContacts(ctx, "", 1)
	if e != nil || count != 3 || len(rows) != 3 {
		t.Fatal(e, count)
	}
	m, _ := db.Contact(ctx, 1)
	if m.NotificationState != "uncertain" {
		t.Fatal(m.NotificationState)
	}
	w.Sender = nil
	w.Step(ctx, now.Add(time.Hour))
	if !w.Metrics.Paused.Load() {
		t.Fatal("missing key not visible")
	}
}

func TestWorkerTemporaryBackoffAndPermanentStop(t *testing.T) {
	ctx := context.Background()
	db, e := store.Open(ctx, filepath.Join(t.TempDir(), "retry.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	now := time.Now()
	_, e = db.CreateContact(ctx, store.ContactMessage{SubmissionHash: strings.Repeat("a", 64), Email: "reader@example.org", Topic: "general", Message: "Synthetic retry", Language: "en", CreatedAt: now.Unix()})
	if e != nil {
		t.Fatal(e)
	}
	sender := &fakeSender{result: Result{State: "retry", Code: "provider_limit", RetryAfter: 10 * time.Minute}}
	w := Worker{Store: db, Sender: sender, Daily: 20, Monthly: 300, Metrics: &Metrics{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	w.Step(ctx, now)
	w.Step(ctx, now.Add(time.Minute))
	if sender.calls != 1 {
		t.Fatal("ignored retry delay")
	}
	sender.result = Result{State: "failed", Code: "provider_rejected"}
	w.Step(ctx, now.Add(10*time.Minute))
	w.Step(ctx, now.Add(24*time.Hour))
	if sender.calls != 2 {
		t.Fatal("retried permanent rejection")
	}
	m, e := db.Contact(ctx, 1)
	if e != nil || m.NotificationState != "failed" {
		t.Fatal("lost message", e)
	}
}
