package gazetteer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResumeSchedulePreservesFailureBackoffAcrossRestart(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "gazetteer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC().Truncate(time.Second)
	source := SourceDefinition{Key: "test", URL: "https://example.test/source"}
	for i := 0; i < 3; i++ {
		id, err := db.BeginRefresh(ctx, RefreshTriggerRetry, []SourceDefinition{source}, now.Add(-time.Hour), now)
		if err != nil {
			t.Fatal(err)
		}
		if err = db.FailRefresh(ctx, id, &source, "http_status", SourceFetchDiagnostic{HTTPStatus: 429}, errors.New("HTTP 429"), now); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.SetNextRefresh(ctx, now.Add(20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	fetcher, _ := NewFetcher(&http.Client{}, "test", db)
	m, err := NewManager(ctx, db, fetcher, []SourceDefinition{source}, 24*time.Hour, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	m.clock = func() time.Time { return now }
	delay, failures, err := m.resumeSchedule(ctx)
	if err != nil || delay != 20*time.Minute || failures != 3 {
		t.Fatalf("resume = %v %d %v", delay, failures, err)
	}
	if next := refreshRetryDelay(failures+1, errors.New("HTTP 429"), now); next != 40*time.Minute {
		t.Fatalf("escalation reset: %v", next)
	}
}

func TestInterruptedRefreshSchedulesRecoveryWithoutImmediateDownload(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "gazetteer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC().Truncate(time.Second)
	id, err := db.BeginRefresh(ctx, RefreshTriggerStartup, []SourceDefinition{{Key: "test"}}, now.Add(-time.Hour), now.Add(7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err = db.StartRefreshSource(ctx, id, "test", now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = db.recoverInterruptedRefreshes(ctx, now); err != nil {
		t.Fatal(err)
	}
	status, err := db.Status(ctx)
	if err != nil || !status.NextRefresh.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("interruption retry=%v %v", status.NextRefresh, err)
	}
	detail, err := db.RefreshDetails(ctx, id)
	if err != nil || detail.Run.SourcesCompleted != 0 || detail.Run.SourcesInterrupted != 1 {
		t.Fatalf("interruption counts=%#v %v", detail.Run, err)
	}
}

func TestRetryAfterAndBackoff(t *testing.T) {
	now := time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value string
		delay time.Duration
	}{
		{"120", 2 * time.Minute}, {now.Add(time.Hour).Format(http.TimeFormat), time.Hour},
		{"999999999", 7 * 24 * time.Hour}, {"-1", 0}, {"nonsense", 0}, {"", 0},
		{now.Add(-time.Hour).Format(http.TimeFormat), 0},
	} {
		at := retryAfterTime(tc.value, now)
		if tc.delay == 0 {
			if !at.IsZero() {
				t.Errorf("invalid header %q accepted", tc.value)
			}
		} else if !at.Equal(now.Add(tc.delay)) {
			t.Errorf("header %q: %v", tc.value, at)
		}
	}
	err := &sourceHTTPError{key: "osm_places", status: 429, retryAt: now.Add(2 * time.Hour)}
	if got := refreshRetryDelay(1, err, now); got != 2*time.Hour {
		t.Fatalf("Retry-After ignored: %v", got)
	}
	if got := refreshRetryDelay(500, errors.New("outage"), now); got != 6*time.Hour {
		t.Fatalf("backoff cap: %v", got)
	}
}

func TestFetcherCarriesProviderRetryAfter(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "gazetteer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC().Truncate(time.Second)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"7200"}}, Body: io.NopCloser(strings.NewReader("busy"))}, nil
	})}
	f, err := NewFetcher(client, "test", db)
	if err != nil {
		t.Fatal(err)
	}
	f.clock = func() time.Time { return now }
	_, _, diagnostic, err := f.FetchWithDiagnostics(ctx, SourceDefinition{Key: "osm_test", DisplayName: "Test", URL: "https://example.test/source", License: "test", Attribution: "test", MinimumRows: 1, MaximumRows: 2, MaximumSize: 1024, Parse: parseMunichStreets})
	var upstream *sourceHTTPError
	if !errors.As(err, &upstream) || diagnostic.HTTPStatus != 429 || !upstream.retryAt.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("provider delay not propagated: %#v %v", diagnostic, err)
	}
}
