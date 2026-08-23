package observability

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

type Metrics struct {
	version                string
	startedAt              time.Time
	feedAttempts           atomic.Uint64
	feedFailures           atomic.Uint64
	feedNotModified        atomic.Uint64
	itemsDiscovered        atomic.Uint64
	articlesFetched        atomic.Uint64
	articleFetchFailures   atomic.Uint64
	parserFailures         atomic.Uint64
	lastFeedSuccess        atomic.Int64
	httpResponses          [6]atomic.Uint64
	sourceFeedResponses    [6]atomic.Uint64
	sourcePageResponses    [6]atomic.Uint64
	processingAttempts     atomic.Uint64
	processingSuccesses    atomic.Uint64
	processingFailures     atomic.Uint64
	processingFailureKinds [4]atomic.Uint64
	processingQueued       atomic.Int64
	processingRunning      atomic.Int64
	processingRetrying     atomic.Int64
	processingReview       atomic.Int64
	processingFailed       atomic.Int64
	processingOldestAge    atomic.Int64
	processorAvailable     atomic.Int64
}

func NewMetrics(version string, startedAt time.Time) *Metrics {
	if version == "" {
		version = "dev"
	}
	metrics := &Metrics{version: version, startedAt: startedAt}
	metrics.processorAvailable.Store(1)
	return metrics
}

func (m *Metrics) RecordFeedAttempt() {
	m.feedAttempts.Add(1)
}

func (m *Metrics) RecordFeedFailure() {
	m.feedFailures.Add(1)
}

func (m *Metrics) RecordFeedSuccess(notModified bool, discovered, fetched, fetchFailures, parserFailures int, at time.Time) {
	if notModified {
		m.feedNotModified.Add(1)
	}
	m.itemsDiscovered.Add(uint64(max(discovered, 0)))
	m.articlesFetched.Add(uint64(max(fetched, 0)))
	m.articleFetchFailures.Add(uint64(max(fetchFailures, 0)))
	m.parserFailures.Add(uint64(max(parserFailures, 0)))
	m.lastFeedSuccess.Store(at.Unix())
}

func (m *Metrics) ObserveHTTPResponse(status int) {
	m.httpResponses[responseClass(status)].Add(1)
}

func (m *Metrics) ObserveSourceResponse(resource string, status int) {
	target := &m.sourcePageResponses
	if resource == "feed" {
		target = &m.sourceFeedResponses
	}
	target[responseClass(status)].Add(1)
}

func (m *Metrics) RecordProcessingAttempt() {
	m.processingAttempts.Add(1)
}

func (m *Metrics) RecordProcessingSuccess() {
	m.processingSuccesses.Add(1)
}

func (m *Metrics) RecordProcessingFailure(kind string) {
	m.processingFailures.Add(1)
	index := map[string]int{"transient": 0, "configuration": 1, "output": 2, "privacy": 3}[kind]
	m.processingFailureKinds[index].Add(1)
}

func (m *Metrics) SetProcessingStats(stats store.ProcessingStats) {
	m.processingQueued.Store(int64(max(stats.Queued, 0)))
	m.processingRunning.Store(int64(max(stats.Running, 0)))
	m.processingRetrying.Store(int64(max(stats.Retrying, 0)))
	m.processingReview.Store(int64(max(stats.NeedsReview, 0)))
	m.processingFailed.Store(int64(max(stats.Failed, 0)))
	m.processingOldestAge.Store(int64(max(stats.OldestPendingAge.Seconds(), 0)))
}

func (m *Metrics) SetProcessorAvailable(available bool) {
	if available {
		m.processorAvailable.Store(1)
		return
	}
	m.processorAvailable.Store(0)
}

func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/metrics" {
			http.NotFound(response, request)
			return
		}
		if request.Method != http.MethodGet {
			response.Header().Set("Allow", http.MethodGet)
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		response.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		m.write(response)
	})
}

func (m *Metrics) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		recorder := &statusWriter{ResponseWriter: response, status: http.StatusOK}
		next.ServeHTTP(recorder, request)
		m.ObserveHTTPResponse(recorder.status)
	})
}

func (m *Metrics) write(writer io.Writer) {
	fmt.Fprintf(writer, "# HELP munichbrief_build_info Build information.\n")
	fmt.Fprintf(writer, "# TYPE munichbrief_build_info gauge\n")
	fmt.Fprintf(writer, "munichbrief_build_info{version=%s} 1\n", strconv.Quote(m.version))
	writeCounter(writer, "munichbrief_feed_sync_attempts_total", "Feed synchronization attempts.", m.feedAttempts.Load())
	writeCounter(writer, "munichbrief_feed_sync_failures_total", "Feed synchronization failures.", m.feedFailures.Load())
	writeCounter(writer, "munichbrief_feed_not_modified_total", "Conditional feed responses reporting no change.", m.feedNotModified.Load())
	writeCounter(writer, "munichbrief_items_discovered_total", "In-window source documents discovered.", m.itemsDiscovered.Load())
	writeCounter(writer, "munichbrief_articles_fetched_total", "Articles fetched and parsed successfully.", m.articlesFetched.Load())
	writeCounter(writer, "munichbrief_article_fetch_failures_total", "Article HTTP fetch failures.", m.articleFetchFailures.Load())
	writeCounter(writer, "munichbrief_parser_failures_total", "Article parser failures.", m.parserFailures.Load())
	fmt.Fprintln(writer, "# HELP munichbrief_last_feed_success_timestamp_seconds Unix timestamp of the last completed feed synchronization.")
	fmt.Fprintln(writer, "# TYPE munichbrief_last_feed_success_timestamp_seconds gauge")
	fmt.Fprintf(writer, "munichbrief_last_feed_success_timestamp_seconds %d\n", m.lastFeedSuccess.Load())
	fmt.Fprintln(writer, "# HELP munichbrief_process_uptime_seconds Process uptime in seconds.")
	fmt.Fprintln(writer, "# TYPE munichbrief_process_uptime_seconds gauge")
	fmt.Fprintf(writer, "munichbrief_process_uptime_seconds %.0f\n", time.Since(m.startedAt).Seconds())
	writeResponseClasses(writer, "munichbrief_http_responses_total", "Reader HTTP responses by status class.", "", &m.httpResponses)
	writeResponseClasses(writer, "munichbrief_source_http_responses_total", "Source HTTP responses by resource and status class.", "feed", &m.sourceFeedResponses)
	writeResponseClasses(writer, "munichbrief_source_http_responses_total", "", "article", &m.sourcePageResponses)
	writeCounter(writer, "munichbrief_processing_attempts_total", "AI presentation processing attempts.", m.processingAttempts.Load())
	writeCounter(writer, "munichbrief_processing_successes_total", "AI presentations generated successfully.", m.processingSuccesses.Load())
	writeCounter(writer, "munichbrief_processing_failures_total", "AI presentation processing failures.", m.processingFailures.Load())
	fmt.Fprintln(writer, "# HELP munichbrief_processing_failures_by_kind_total AI processing failures by safe machine-readable category.")
	fmt.Fprintln(writer, "# TYPE munichbrief_processing_failures_by_kind_total counter")
	for index, kind := range []string{"transient", "configuration", "output", "privacy"} {
		fmt.Fprintf(writer, "munichbrief_processing_failures_by_kind_total{kind=%q} %d\n", kind, m.processingFailureKinds[index].Load())
	}
	fmt.Fprintln(writer, "# HELP munichbrief_processing_jobs AI processing jobs by state.")
	fmt.Fprintln(writer, "# TYPE munichbrief_processing_jobs gauge")
	for state, value := range map[string]int64{
		"queued": m.processingQueued.Load(), "running": m.processingRunning.Load(),
		"retrying": m.processingRetrying.Load(), "needs_review": m.processingReview.Load(),
		"failed": m.processingFailed.Load(),
	} {
		fmt.Fprintf(writer, "munichbrief_processing_jobs{state=%q} %d\n", state, value)
	}
	fmt.Fprintln(writer, "# HELP munichbrief_processing_oldest_job_age_seconds Age of the oldest pending AI processing job.")
	fmt.Fprintln(writer, "# TYPE munichbrief_processing_oldest_job_age_seconds gauge")
	fmt.Fprintf(writer, "munichbrief_processing_oldest_job_age_seconds %d\n", m.processingOldestAge.Load())
	fmt.Fprintln(writer, "# HELP munichbrief_ai_processor_available Whether the AI processor circuit is closed and available.")
	fmt.Fprintln(writer, "# TYPE munichbrief_ai_processor_available gauge")
	fmt.Fprintf(writer, "munichbrief_ai_processor_available %d\n", m.processorAvailable.Load())
	writeCounter(writer, "munichbrief_retention_deletions_total", "Records removed by retention processing.", 0)
}

func writeCounter(writer io.Writer, name, help string, value uint64) {
	fmt.Fprintf(writer, "# HELP %s %s\n", name, help)
	fmt.Fprintf(writer, "# TYPE %s counter\n", name)
	fmt.Fprintf(writer, "%s %d\n", name, value)
}

func writeResponseClasses(writer io.Writer, name, help, resource string, values *[6]atomic.Uint64) {
	if help != "" {
		fmt.Fprintf(writer, "# HELP %s %s\n", name, help)
		fmt.Fprintf(writer, "# TYPE %s counter\n", name)
	}
	classes := []string{"error", "1xx", "2xx", "3xx", "4xx", "5xx"}
	for index, class := range classes {
		if resource == "" {
			fmt.Fprintf(writer, "%s{class=%q} %d\n", name, class, values[index].Load())
		} else {
			fmt.Fprintf(writer, "%s{resource=%q,class=%q} %d\n", name, resource, class, values[index].Load())
		}
	}
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(contents []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(contents)
}

func responseClass(status int) int {
	if status < 100 || status > 599 {
		return 0
	}
	return status / 100
}
