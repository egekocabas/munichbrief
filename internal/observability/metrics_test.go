package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

func TestMetricsExposeSynchronizationAndHTTPState(t *testing.T) {
	metrics := NewMetrics("test-version", time.Now().Add(-time.Minute))
	metrics.RecordFeedAttempt()
	metrics.RecordFeedFailure()
	metrics.RecordFeedSuccess(true, 2, 1, 3, 4, time.Unix(1234, 0))
	metrics.RecordFeedDuration(1500 * time.Millisecond)
	metrics.SetNextFeedSync(time.Unix(2345, 0))
	metrics.ObserveSourceResponse("feed", http.StatusNotModified)
	metrics.ObserveSourceResponse("article", 0)
	metrics.SetProcessorAvailable(false)
	metrics.SetProcessingWindowOpen(true)

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
		"munichbrief_next_feed_sync_timestamp_seconds 2345",
		"munichbrief_feed_sync_duration_seconds_sum 1.500000",
		"munichbrief_feed_sync_duration_seconds_count 1",
		`munichbrief_http_responses_total{class="5xx"} 1`,
		`munichbrief_source_http_responses_total{resource="feed",class="3xx"} 1`,
		`munichbrief_source_http_responses_total{resource="article",class="error"} 1`,
		"munichbrief_last_processing_success_timestamp_seconds 0",
		"munichbrief_ai_processor_available 0",
		"munichbrief_ai_processing_window_open 1",
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

func TestSetLastFeedSuccessRestoresPersistedTimestamp(t *testing.T) {
	metrics := NewMetrics("test", time.Now())
	metrics.SetLastFeedSuccess(time.Unix(1234, 0))

	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(recorder.Body.String(), "munichbrief_last_feed_success_timestamp_seconds 1234") {
		t.Fatal("metrics did not expose the restored feed success timestamp")
	}
}

func TestMetricsExposeStagedPipelineState(t *testing.T) {
	metrics := NewMetrics("test", time.Now())
	metrics.RecordPipelineAttempt("incident_metadata")
	metrics.RecordPipelineAttempt("german_presentation")
	metrics.RecordPipelineAttempt("category_verification/default")
	metrics.RecordPipelineAttempt("translation/en")
	metrics.RecordPipelineSuccess("incident_metadata", time.Unix(4567, 0))
	metrics.RecordPipelineSuccess("category_verification/default", time.Unix(4568, 0))
	metrics.RecordPipelineFailure("translation/en", "output")
	metrics.RecordPipelineDuration("category_verification/default", 1250*time.Millisecond)
	metrics.RecordPipelineDuration("translation/en", 1750*time.Millisecond)
	metrics.SetPipelineSnapshot(store.PipelineSnapshot{
		ActiveCycle:   &store.PipelineCycle{Kind: "manual", ActiveStep: 1},
		ActiveStepKey: "german_presentation",
		Steps: []store.StepQueueStats{
			{StepKey: "incident_metadata", Queued: 2, Succeeded: 3},
			{StepKey: "german_presentation", Retrying: 1},
		},
		PostProcessing: []store.PostProcessingQueueStats{
			{ProcessorKey: "category_verification", ScopeKey: "default", Pending: 2, Running: 1, Retrying: 3, NeedsReview: 4, Failed: 5, Succeeded: 6, Counters: map[string]int{"corrected": 7}},
			{ProcessorKey: "translation", ScopeKey: "en", Running: 1, NeedsReview: 4},
		},
	})

	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, expected := range []string{
		`munichbrief_pipeline_attempts_total{step="incident_metadata"} 1`,
		`munichbrief_pipeline_attempts_total{step="german_presentation"} 1`,
		`munichbrief_pipeline_attempts_total{step="category_verification/default"} 1`,
		`munichbrief_pipeline_successes_total{step="incident_metadata"} 1`,
		`munichbrief_pipeline_successes_total{step="category_verification/default"} 1`,
		`munichbrief_pipeline_failures_total{step="translation/en"} 1`,
		`munichbrief_pipeline_failures_by_kind_total{step="translation/en",kind="output"} 1`,
		`munichbrief_pipeline_duration_seconds_sum{step="category_verification/default"} 1.250000`,
		`munichbrief_pipeline_duration_seconds_sum{step="translation/en"} 1.750000`,
		`munichbrief_pipeline_jobs{step="incident_metadata",state="queued"} 2`,
		`munichbrief_pipeline_jobs{step="german_presentation",state="retrying"} 1`,
		`munichbrief_pipeline_jobs{step="category_verification/default",state="queued"} 2`,
		`munichbrief_pipeline_jobs{step="category_verification/default",state="running"} 1`,
		`munichbrief_pipeline_jobs{step="category_verification/default",state="retrying"} 3`,
		`munichbrief_pipeline_jobs{step="category_verification/default",state="needs_review"} 4`,
		`munichbrief_pipeline_jobs{step="category_verification/default",state="failed"} 5`,
		`munichbrief_pipeline_jobs{step="category_verification/default",state="succeeded"} 6`,
		`munichbrief_pipeline_jobs{step="translation/en",state="needs_review"} 4`,
		`munichbrief_post_processing_results{processor="category_verification",scope="default",counter="corrected"} 7`,
		`munichbrief_pipeline_active_cycle{kind="manual"} 1`,
		`munichbrief_pipeline_active_step{step="german_presentation"} 1`,
		"munichbrief_last_processing_success_timestamp_seconds 4568",
	} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Errorf("metrics body does not contain %q", expected)
		}
	}
}
