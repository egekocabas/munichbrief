package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsExposeSynchronizationAndHTTPState(t *testing.T) {
	metrics := NewMetrics("test-version", time.Now().Add(-time.Minute))
	metrics.RecordFeedAttempt()
	metrics.RecordFeedFailure()
	metrics.RecordFeedSuccess(true, 2, 1, 3, 4, time.Unix(1234, 0))
	metrics.ObserveSourceResponse("feed", http.StatusNotModified)
	metrics.ObserveSourceResponse("article", 0)

	wrapped := metrics.Wrap(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "unavailable", http.StatusServiceUnavailable)
	}))
	wrapped.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", recorder.Code)
	}
	for _, expected := range []string{
		`munichbrief_build_info{version="test-version"} 1`,
		"munichbrief_feed_sync_attempts_total 1",
		"munichbrief_feed_sync_failures_total 1",
		"munichbrief_feed_not_modified_total 1",
		"munichbrief_items_discovered_total 2",
		"munichbrief_articles_fetched_total 1",
		"munichbrief_article_fetch_failures_total 3",
		"munichbrief_parser_failures_total 4",
		"munichbrief_last_feed_success_timestamp_seconds 1234",
		`munichbrief_http_responses_total{class="5xx"} 1`,
		`munichbrief_source_http_responses_total{resource="feed",class="3xx"} 1`,
		`munichbrief_source_http_responses_total{resource="article",class="error"} 1`,
		"munichbrief_processing_queue_depth 0",
		"munichbrief_retention_deletions_total 0",
	} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Errorf("metrics body does not contain %q", expected)
		}
	}
}

func TestMetricsEndpointRejectsOtherMethodsAndPaths(t *testing.T) {
	metrics := NewMetrics("test", time.Now())
	for _, test := range []struct {
		method string
		path   string
		status int
	}{
		{method: http.MethodPost, path: "/metrics", status: http.StatusMethodNotAllowed},
		{method: http.MethodGet, path: "/other", status: http.StatusNotFound},
	} {
		recorder := httptest.NewRecorder()
		metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
		if recorder.Code != test.status {
			t.Errorf("%s %s status = %d, want %d", test.method, test.path, recorder.Code, test.status)
		}
	}
}
