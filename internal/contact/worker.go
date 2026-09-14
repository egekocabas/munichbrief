// Package contact delivers operator notifications independently of AI work.
package contact

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

const Address = "contact@munichbrief.de"
const Endpoint = "https://eu-api.smtp2go.com/v3/email/send"

type Result struct {
	State, Code, ProviderID string
	RetryAfter              time.Duration
}
type Sender interface {
	Send(context.Context, store.ContactMessage) Result
}
type SMTP2GO struct {
	APIKey string
	Client *http.Client
}

func (s SMTP2GO) Send(ctx context.Context, m store.ContactMessage) Result {
	subject := fmt.Sprintf("MunichBrief enquiry #%d · %s", m.ID, m.Topic)
	if m.IsTest {
		subject = fmt.Sprintf("MunichBrief test email #%d", m.ID)
	}
	body, _ := json.Marshal(map[string]any{
		"sender": Address, "to": []string{Address},
		"subject":        subject,
		"text_body":      fmt.Sprintf("Enquiry #%d\nTopic: %s\nReceived: %s\n\n%s", m.ID, m.Topic, time.Unix(m.CreatedAt, 0).UTC().Format(time.RFC3339), m.Message),
		"custom_headers": []map[string]string{{"header": "Reply-To", "value": m.Email}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, Endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{State: "failed", Code: "request"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Smtp2go-Api-Key", s.APIKey)
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, err := client.Do(req)
	if err != nil {
		return Result{State: "uncertain", Code: "transport"}
	}
	defer response.Body.Close()
	var payload struct {
		Data struct {
			Succeeded, Failed int
			EmailID           string `json:"email_id"`
			ErrorCode         string `json:"error_code"`
		}
	}
	decodeErr := json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&payload)
	retryAfter := time.Minute
	if n, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && n > 0 {
		retryAfter = min(time.Duration(min(n, 86400))*time.Second, 24*time.Hour)
	} else if date, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil {
		retryAfter = max(time.Minute, min(time.Until(date), 24*time.Hour))
	}
	code := strings.ToLower(payload.Data.ErrorCode)
	if response.StatusCode == 429 || strings.Contains(code, "ratelimit") || strings.Contains(code, "quota") {
		return Result{State: "retry", Code: "provider_limit", RetryAfter: retryAfter}
	}
	if response.StatusCode >= 500 {
		return Result{State: "retry", Code: "provider_unavailable", RetryAfter: retryAfter}
	}
	if response.StatusCode >= 400 || (decodeErr == nil && (payload.Data.Failed > 0 || payload.Data.ErrorCode != "")) {
		return Result{State: "failed", Code: "provider_rejected"}
	}
	if decodeErr != nil || response.StatusCode != 200 || payload.Data.Succeeded != 1 {
		return Result{State: "uncertain", Code: "unexpected_response"}
	}
	// Never persist arbitrary response bodies or provider errors that might echo input.
	providerID := payload.Data.EmailID
	if len(providerID) > 128 {
		providerID = ""
	}
	return Result{State: "accepted", Code: "accepted", ProviderID: providerID}
}

type Metrics struct {
	Received, Failures, CleanupFailures atomic.Uint64
	Pending, Problems, OldestAge        atomic.Int64
	Paused                              atomic.Bool
}

func (m *Metrics) Write(w io.Writer) {
	fmt.Fprintf(w, "# TYPE munichbrief_contact_received_total counter\nmunichbrief_contact_received_total %d\n# TYPE munichbrief_contact_notification_failures_total counter\nmunichbrief_contact_notification_failures_total %d\n# TYPE munichbrief_contact_cleanup_failures_total counter\nmunichbrief_contact_cleanup_failures_total %d\n", m.Received.Load(), m.Failures.Load(), m.CleanupFailures.Load())
	fmt.Fprintf(w, "# TYPE munichbrief_contact_pending gauge\nmunichbrief_contact_pending %d\n# TYPE munichbrief_contact_problems gauge\nmunichbrief_contact_problems %d\n# TYPE munichbrief_contact_oldest_pending_seconds gauge\nmunichbrief_contact_oldest_pending_seconds %d\n", m.Pending.Load(), m.Problems.Load(), m.OldestAge.Load())
	paused := 0
	if m.Paused.Load() {
		paused = 1
	}
	fmt.Fprintf(w, "# TYPE munichbrief_contact_notifications_paused gauge\nmunichbrief_contact_notifications_paused %d\n", paused)
}

type Worker struct {
	Store          *store.Store
	Sender         Sender
	Daily, Monthly int
	Metrics        *Metrics
	Logger         *slog.Logger
	nextCleanup    time.Time
}

func (w *Worker) Run(ctx context.Context) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		if err := w.Store.RecoverContactNotifications(ctx); err == nil {
			break
		}
		w.Metrics.Paused.Store(true)
		w.Metrics.Failures.Add(1)
		w.Logger.Error("contact notification recovery failed")
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
	for {
		w.Step(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func (w *Worker) Step(ctx context.Context, now time.Time) {
	if !now.Before(w.nextCleanup) {
		if err := w.Store.PurgeContacts(ctx, now); err != nil {
			w.Metrics.CleanupFailures.Add(1)
			w.Logger.Error("contact retention cleanup failed")
		} else {
			w.nextCleanup = now.Add(time.Hour)
		}
	}
	if stats, err := w.Store.ContactStats(ctx); err == nil {
		w.Metrics.Pending.Store(int64(stats.Pending))
		w.Metrics.Problems.Store(int64(stats.Problems))
		age := int64(0)
		if stats.Oldest > 0 {
			age = max(0, now.Unix()-stats.Oldest)
		}
		w.Metrics.OldestAge.Store(age)
	}
	if w.Sender == nil {
		w.Metrics.Paused.Store(true)
		return
	}
	m, err := w.Store.ClaimContact(ctx, now, w.Daily, w.Monthly)
	if errors.Is(err, store.ErrContactBudget) || errors.Is(err, store.ErrContactPaused) {
		w.Metrics.Paused.Store(true)
		return
	}
	w.Metrics.Paused.Store(false)
	if errors.Is(err, store.ErrNotFound) {
		return
	}
	if err != nil {
		w.Logger.Error("contact notification claim failed")
		return
	}
	result := w.Sender.Send(ctx, m)
	if result.State != "accepted" {
		w.Metrics.Failures.Add(1)
	}
	delay := max(result.RetryAfter, min(time.Duration(1<<min(m.NotificationAttempts, 8))*time.Minute, 6*time.Hour))
	// Always reconcile a cancelled request's in-flight state before shutdown.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := w.Store.FinishContactNotification(finishCtx, m.ID, result.State, result.Code, result.ProviderID, now.Add(delay)); err != nil {
		w.Logger.Error("contact notification completion failed")
	}
}
