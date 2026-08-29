package observability

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

// Metrics stores fixed-cardinality counters and gauges using atomics so
// instrumentation does not serialize request or worker paths.
type Metrics struct {
	version               string
	startedAt             time.Time
	feedAttempts          atomic.Uint64
	feedFailures          atomic.Uint64
	feedNotModified       atomic.Uint64
	itemsDiscovered       atomic.Uint64
	articlesFetched       atomic.Uint64
	articleFetchFailures  atomic.Uint64
	parserFailures        atomic.Uint64
	lastFeedSuccess       atomic.Int64
	nextFeedSync          atomic.Int64
	feedDurationNanos     atomic.Uint64
	feedDurationCount     atomic.Uint64
	httpResponses         [6]atomic.Uint64
	sourceFeedResponses   [6]atomic.Uint64
	sourcePageResponses   [6]atomic.Uint64
	processorAvailable    atomic.Int64
	processingWindowOpen  atomic.Int64
	lastProcessingSuccess atomic.Int64
	pipelineMu            sync.RWMutex
	pipeline              map[string]*pipelineStepMetrics
	postCounters          map[postCounterKey]int64
	pipelineActiveCycle   atomic.Int64
	pipelineActiveStep    atomic.Value
	pipelineActiveKind    [3]atomic.Int64
}

type pipelineStepMetrics struct {
	attempts, successes, failures, durationNanos, durationCount atomic.Uint64
	queued, running, retrying, review, failed, succeeded        atomic.Int64
}

type postCounterKey struct {
	processor string
	scope     string
	counter   string
}

// NewMetrics creates an empty registry for one process instance.
func NewMetrics(version string, startedAt time.Time) *Metrics {
	if version == "" {
		version = "dev"
	}
	metrics := &Metrics{version: version, startedAt: startedAt, pipeline: make(map[string]*pipelineStepMetrics), postCounters: make(map[postCounterKey]int64)}
	metrics.pipelineActiveStep.Store("")
	metrics.pipelineStep("incident_metadata")
	metrics.pipelineStep("german_presentation")
	metrics.processorAvailable.Store(1)
	return metrics
}

func (m *Metrics) RecordFeedAttempt() {
	m.feedAttempts.Add(1)
}

func (m *Metrics) RecordFeedFailure() {
	m.feedFailures.Add(1)
}

func (m *Metrics) SetLastFeedSuccess(at time.Time) {
	m.lastFeedSuccess.Store(at.Unix())
}

func (m *Metrics) RecordFeedSuccess(notModified bool, discovered, fetched, fetchFailures, parserFailures int, at time.Time) {
	if notModified {
		m.feedNotModified.Add(1)
	}
	m.itemsDiscovered.Add(uint64(max(discovered, 0)))
	m.articlesFetched.Add(uint64(max(fetched, 0)))
	m.articleFetchFailures.Add(uint64(max(fetchFailures, 0)))
	m.parserFailures.Add(uint64(max(parserFailures, 0)))
	m.SetLastFeedSuccess(at)
}

func (m *Metrics) RecordFeedDuration(duration time.Duration) {
	m.feedDurationNanos.Add(uint64(max(duration.Nanoseconds(), 0)))
	m.feedDurationCount.Add(1)
}

func (m *Metrics) SetNextFeedSync(at time.Time) {
	m.nextFeedSync.Store(at.Unix())
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

func (m *Metrics) SetProcessorAvailable(available bool) {
	if available {
		m.processorAvailable.Store(1)
		return
	}
	m.processorAvailable.Store(0)
}

func (m *Metrics) SetProcessingWindowOpen(open bool) {
	if open {
		m.processingWindowOpen.Store(1)
		return
	}
	m.processingWindowOpen.Store(0)
}

func (m *Metrics) pipelineStep(step string) *pipelineStepMetrics {
	m.pipelineMu.RLock()
	metrics := m.pipeline[step]
	m.pipelineMu.RUnlock()
	if metrics != nil {
		return metrics
	}
	m.pipelineMu.Lock()
	defer m.pipelineMu.Unlock()
	if metrics = m.pipeline[step]; metrics == nil {
		metrics = &pipelineStepMetrics{}
		m.pipeline[step] = metrics
	}
	return metrics
}

func (m *Metrics) pipelineSteps() []string {
	m.pipelineMu.RLock()
	steps := make([]string, 0, len(m.pipeline))
	for step := range m.pipeline {
		steps = append(steps, step)
	}
	m.pipelineMu.RUnlock()
	sort.Strings(steps)
	return steps
}

func (m *Metrics) RecordPipelineAttempt(step string) {
	m.pipelineStep(step).attempts.Add(1)
}

func (m *Metrics) RecordPipelineSuccess(step string, at time.Time) {
	m.pipelineStep(step).successes.Add(1)
	m.lastProcessingSuccess.Store(at.Unix())
}

func (m *Metrics) RecordPipelineFailure(step, _ string) {
	m.pipelineStep(step).failures.Add(1)
}

func (m *Metrics) RecordPipelineDuration(step string, duration time.Duration) {
	metrics := m.pipelineStep(step)
	metrics.durationNanos.Add(uint64(max(duration.Nanoseconds(), 0)))
	metrics.durationCount.Add(1)
}

func (m *Metrics) SetPipelineSnapshot(snapshot store.PipelineSnapshot) {
	m.pipelineMu.Lock()
	m.postCounters = make(map[postCounterKey]int64)
	for _, stats := range snapshot.PostProcessing {
		for counter, value := range stats.Counters {
			m.postCounters[postCounterKey{processor: stats.ProcessorKey, scope: stats.ScopeKey, counter: counter}] = int64(max(value, 0))
		}
	}
	m.pipelineMu.Unlock()
	for _, step := range m.pipelineSteps() {
		metrics := m.pipelineStep(step)
		metrics.queued.Store(0)
		metrics.running.Store(0)
		metrics.retrying.Store(0)
		metrics.review.Store(0)
		metrics.failed.Store(0)
		metrics.succeeded.Store(0)
	}
	for _, stats := range snapshot.Steps {
		metrics := m.pipelineStep(stats.StepKey)
		metrics.queued.Store(int64(max(stats.Queued, 0)))
		metrics.running.Store(int64(max(stats.Running, 0)))
		metrics.retrying.Store(int64(max(stats.Retrying, 0)))
		metrics.review.Store(int64(max(stats.NeedsReview, 0)))
		metrics.failed.Store(int64(max(stats.Failed, 0)))
		metrics.succeeded.Store(int64(max(stats.Succeeded, 0)))
	}
	for _, stats := range snapshot.PostProcessing {
		metrics := m.pipelineStep(stats.ProcessorKey + "/" + stats.ScopeKey)
		metrics.queued.Store(int64(max(stats.Pending, 0)))
		metrics.running.Store(int64(max(stats.Running, 0)))
		metrics.retrying.Store(int64(max(stats.Retrying, 0)))
		metrics.review.Store(int64(max(stats.NeedsReview, 0)))
		metrics.failed.Store(int64(max(stats.Failed, 0)))
		metrics.succeeded.Store(int64(max(stats.Succeeded, 0)))
	}
	for index := range m.pipelineActiveKind {
		m.pipelineActiveKind[index].Store(0)
	}
	if snapshot.ActiveCycle == nil {
		m.pipelineActiveCycle.Store(0)
		m.pipelineActiveStep.Store("")
		return
	}
	m.pipelineActiveCycle.Store(1)
	m.pipelineActiveStep.Store(snapshot.ActiveStepKey)
	for index, kind := range []string{"scheduled", "manual", "continuation"} {
		if snapshot.ActiveCycle.Kind == kind {
			m.pipelineActiveKind[index].Store(1)
		}
	}
}

// Handler exposes the registry in the Prometheus text format.
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

// Wrap records HTTP status classes for a handler without changing its response.
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
	fmt.Fprintln(writer, "# HELP munichbrief_next_feed_sync_timestamp_seconds Unix timestamp of the next scheduled feed synchronization.")
	fmt.Fprintln(writer, "# TYPE munichbrief_next_feed_sync_timestamp_seconds gauge")
	fmt.Fprintf(writer, "munichbrief_next_feed_sync_timestamp_seconds %d\n", m.nextFeedSync.Load())
	writeDurationSummary(writer, "munichbrief_feed_sync_duration_seconds", "Feed synchronization duration.", m.feedDurationNanos.Load(), m.feedDurationCount.Load())
	fmt.Fprintln(writer, "# HELP munichbrief_process_uptime_seconds Process uptime in seconds.")
	fmt.Fprintln(writer, "# TYPE munichbrief_process_uptime_seconds gauge")
	fmt.Fprintf(writer, "munichbrief_process_uptime_seconds %.0f\n", time.Since(m.startedAt).Seconds())
	writeResponseClasses(writer, "munichbrief_http_responses_total", "Reader HTTP responses by status class.", "", &m.httpResponses)
	writeResponseClasses(writer, "munichbrief_source_http_responses_total", "Source HTTP responses by resource and status class.", "feed", &m.sourceFeedResponses)
	writeResponseClasses(writer, "munichbrief_source_http_responses_total", "", "article", &m.sourcePageResponses)
	fmt.Fprintln(writer, "# HELP munichbrief_last_processing_success_timestamp_seconds Unix timestamp of the last successful AI presentation.")
	fmt.Fprintln(writer, "# TYPE munichbrief_last_processing_success_timestamp_seconds gauge")
	fmt.Fprintf(writer, "munichbrief_last_processing_success_timestamp_seconds %d\n", m.lastProcessingSuccess.Load())
	fmt.Fprintln(writer, "# HELP munichbrief_ai_processor_available Whether the AI processor circuit is closed and available.")
	fmt.Fprintln(writer, "# TYPE munichbrief_ai_processor_available gauge")
	fmt.Fprintf(writer, "munichbrief_ai_processor_available %d\n", m.processorAvailable.Load())
	fmt.Fprintln(writer, "# HELP munichbrief_ai_processing_window_open Whether new AI requests may start under the configured schedule.")
	fmt.Fprintln(writer, "# TYPE munichbrief_ai_processing_window_open gauge")
	fmt.Fprintf(writer, "munichbrief_ai_processing_window_open %d\n", m.processingWindowOpen.Load())
	fmt.Fprintln(writer, "# HELP munichbrief_pipeline_attempts_total AI attempts by canonical or independent processing step.")
	fmt.Fprintln(writer, "# TYPE munichbrief_pipeline_attempts_total counter")
	fmt.Fprintln(writer, "# HELP munichbrief_pipeline_successes_total Successful AI jobs by canonical or independent processing step.")
	fmt.Fprintln(writer, "# TYPE munichbrief_pipeline_successes_total counter")
	fmt.Fprintln(writer, "# HELP munichbrief_pipeline_failures_total Failed AI attempts by canonical or independent processing step.")
	fmt.Fprintln(writer, "# TYPE munichbrief_pipeline_failures_total counter")
	fmt.Fprintln(writer, "# HELP munichbrief_pipeline_duration_seconds AI attempt duration by canonical or independent processing step.")
	fmt.Fprintln(writer, "# TYPE munichbrief_pipeline_duration_seconds summary")
	fmt.Fprintln(writer, "# HELP munichbrief_pipeline_jobs AI jobs by canonical or independent processing step and bounded state.")
	fmt.Fprintln(writer, "# TYPE munichbrief_pipeline_jobs gauge")
	for _, step := range m.pipelineSteps() {
		metrics := m.pipelineStep(step)
		fmt.Fprintf(writer, "munichbrief_pipeline_attempts_total{step=%q} %d\n", step, metrics.attempts.Load())
		fmt.Fprintf(writer, "munichbrief_pipeline_successes_total{step=%q} %d\n", step, metrics.successes.Load())
		fmt.Fprintf(writer, "munichbrief_pipeline_failures_total{step=%q} %d\n", step, metrics.failures.Load())
		fmt.Fprintf(writer, "munichbrief_pipeline_duration_seconds_sum{step=%q} %.6f\n", step, float64(metrics.durationNanos.Load())/float64(time.Second))
		fmt.Fprintf(writer, "munichbrief_pipeline_duration_seconds_count{step=%q} %d\n", step, metrics.durationCount.Load())
		for state, value := range map[string]int64{"queued": metrics.queued.Load(), "running": metrics.running.Load(), "retrying": metrics.retrying.Load(), "needs_review": metrics.review.Load(), "failed": metrics.failed.Load(), "succeeded": metrics.succeeded.Load()} {
			fmt.Fprintf(writer, "munichbrief_pipeline_jobs{step=%q,state=%q} %d\n", step, state, value)
		}
	}
	fmt.Fprintln(writer, "# HELP munichbrief_post_processing_results Successful post-processing results matching a registered aggregate counter.")
	fmt.Fprintln(writer, "# TYPE munichbrief_post_processing_results gauge")
	m.pipelineMu.RLock()
	counterKeys := make([]postCounterKey, 0, len(m.postCounters))
	for key := range m.postCounters {
		counterKeys = append(counterKeys, key)
	}
	sort.Slice(counterKeys, func(i, j int) bool {
		if counterKeys[i].processor != counterKeys[j].processor {
			return counterKeys[i].processor < counterKeys[j].processor
		}
		if counterKeys[i].scope != counterKeys[j].scope {
			return counterKeys[i].scope < counterKeys[j].scope
		}
		return counterKeys[i].counter < counterKeys[j].counter
	})
	for _, key := range counterKeys {
		fmt.Fprintf(writer, "munichbrief_post_processing_results{processor=%q,scope=%q,counter=%q} %d\n", key.processor, key.scope, key.counter, m.postCounters[key])
	}
	m.pipelineMu.RUnlock()
	fmt.Fprintln(writer, "# HELP munichbrief_pipeline_active_cycle Whether a staged processing cycle is active.")
	fmt.Fprintln(writer, "# TYPE munichbrief_pipeline_active_cycle gauge")
	for index, kind := range []string{"scheduled", "manual", "continuation"} {
		fmt.Fprintf(writer, "munichbrief_pipeline_active_cycle{kind=%q} %d\n", kind, m.pipelineActiveKind[index].Load())
	}
	fmt.Fprintln(writer, "# HELP munichbrief_pipeline_active_step Whether a registered step is active.")
	fmt.Fprintln(writer, "# TYPE munichbrief_pipeline_active_step gauge")
	activeStep, _ := m.pipelineActiveStep.Load().(string)
	for _, step := range m.pipelineSteps() {
		active := int64(0)
		if m.pipelineActiveCycle.Load() == 1 && activeStep == step {
			active = 1
		}
		fmt.Fprintf(writer, "munichbrief_pipeline_active_step{step=%q} %d\n", step, active)
	}
	writeCounter(writer, "munichbrief_retention_deletions_total", "Records removed by retention processing.", 0)
}

func writeDurationSummary(writer io.Writer, name, help string, nanoseconds, count uint64) {
	fmt.Fprintf(writer, "# HELP %s %s\n", name, help)
	fmt.Fprintf(writer, "# TYPE %s summary\n", name)
	fmt.Fprintf(writer, "%s_sum %.6f\n", name, float64(nanoseconds)/float64(time.Second))
	fmt.Fprintf(writer, "%s_count %d\n", name, count)
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
