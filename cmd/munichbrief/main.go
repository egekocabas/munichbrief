package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/egekocabas/munichbrief/internal/config"
	"github.com/egekocabas/munichbrief/internal/ingest"
	"github.com/egekocabas/munichbrief/internal/observability"
	"github.com/egekocabas/munichbrief/internal/processing"
	"github.com/egekocabas/munichbrief/internal/source"
	"github.com/egekocabas/munichbrief/internal/store"
	"github.com/egekocabas/munichbrief/internal/web"
)

var (
	version     = "dev"
	buildCommit = "dev"
	buildTime   = "dev"
)

const (
	liveSyncInterval     = 6 * time.Hour
	liveSyncJitter       = 30 * time.Minute
	liveSyncRetryInitial = time.Minute
	liveSyncRetryMaximum = 30 * time.Minute
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger, os.Args[1:]); err != nil {
		logger.Error("application stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, arguments []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	command := "serve"
	if len(arguments) > 0 {
		command = arguments[0]
		arguments = arguments[1:]
	}

	switch command {
	case "serve":
		return runServer(ctx, logger, cfg)
	case "migrate":
		return runMigrate(ctx, logger, cfg)
	case "sync":
		return runOneShotSync(ctx, logger, cfg)
	case "backup":
		return runBackup(ctx, cfg, arguments, os.Stdout)
	case "ai-process", "ai-retry":
		return runAIProcess(ctx, logger, cfg, arguments)
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q; use munichbrief help", command)
	}
}

func runServer(ctx context.Context, logger *slog.Logger, cfg config.Config) error {
	build, err := injectedBuildInfo()
	if err != nil {
		return err
	}
	if cfg.PresentationMode == "review" {
		logger.Warn("review presentation mode exposes stored original text and must remain access-restricted")
	}
	if cfg.AIEnabled && strings.HasPrefix(cfg.OllamaBaseURL, "http://") && !strings.Contains(cfg.OllamaBaseURL, "127.0.0.1") && !strings.Contains(cfg.OllamaBaseURL, "localhost") {
		logger.Warn("remote Ollama traffic is unencrypted; restrict this deployment and use an encrypted transport before public launch")
	}
	database, err := store.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.EnsurePipelineSteps(ctx, processing.ModelSettingKeys(), time.Now()); err != nil {
		return err
	}

	metrics := observability.NewMetrics(version, time.Now())
	var berlinLocation *time.Location
	if cfg.SourceMode == "live" || cfg.AIEnabled {
		berlinLocation, err = time.LoadLocation("Europe/Berlin")
		if err != nil {
			return fmt.Errorf("load Europe/Berlin timezone: %w", err)
		}
	}
	var liveSyncer *ingest.Syncer
	if cfg.SourceMode == "fixture" {
		documents, err := source.NewFixtureProvider().Load(ctx)
		if err != nil {
			return err
		}
		if err := database.UpsertDocuments(ctx, documents, time.Now()); err != nil {
			return err
		}
	} else {
		if err := restoreFeedSuccessMetric(ctx, database, metrics); err != nil {
			return err
		}
		liveSyncer, err = newLiveSyncer(database, cfg, metrics.ObserveSourceResponse, logger)
		if err != nil {
			return err
		}
	}
	var aiWorker *processing.PipelineWorker
	if cfg.AIEnabled {
		catalog, err := processing.NewOllamaModelCatalog(cfg.OllamaBaseURL, 5*time.Second, nil)
		if err != nil {
			return err
		}
		snapshot := catalog.Refresh(ctx)
		if snapshot.Err != nil {
			logger.Warn("Ollama model catalog unavailable; AI processing is paused", "error", snapshot.Err)
		} else {
			logger.Info("Ollama model catalog loaded", "models", len(snapshot.Models))
		}
		go catalog.Run(ctx)
		provider, err := processing.NewOllamaGeneratorProvider(
			cfg.OllamaBaseURL,
			cfg.AITimeout,
			cfg.AIContextSize,
			nil,
		)
		if err != nil {
			return err
		}
		schedule := processing.Schedule{
			Immediate: cfg.AIImmediate,
			Location:  berlinLocation,
			Start:     cfg.AIWindowStart,
			End:       cfg.AIWindowEnd,
		}
		aiWorker, err = processing.NewPipelineWorker(database, provider, catalog, metrics, logger, cfg.AIInterval, time.Now, schedule, cfg.SourceMode)
		if err != nil {
			return err
		}
	}
	processor := webProcessor(aiWorker)

	webServer, err := web.NewWithOptions(database, logger, web.Options{
		PageSize: cfg.PageSize, SourceMode: cfg.SourceMode, PresentationMode: cfg.PresentationMode,
		PromptVersion: processing.PipelineVersion, SecureCookies: cfg.SecureCookies,
		AdminEnabled: cfg.AdminEnabled, PublicHosts: cfg.PublicHosts, CanonicalOrigin: cfg.CanonicalOrigin, Processor: processor,
		Build: build,
	})
	if err != nil {
		return err
	}
	httpServer := configuredServer(cfg.Address, metrics.Wrap(webServer.Handler()))
	metricsServer := configuredServer(cfg.MetricsAddress, metrics.Handler())

	serveErrors := make(chan error, 2)
	go serve(httpServer, "reader", cfg.Address, cfg.SourceMode, logger, serveErrors)
	go serve(metricsServer, "metrics", cfg.MetricsAddress, cfg.SourceMode, logger, serveErrors)
	if liveSyncer != nil {
		go runLiveSyncLoop(ctx, berlinLocation, liveSyncer, metrics, logger)
	}
	if aiWorker != nil {
		logger.Info("AI processing worker started",
			"base_url", cfg.OllamaBaseURL,
			"immediate", cfg.AIImmediate,
			"window_start", cfg.AIWindowStart,
			"window_end", cfg.AIWindowEnd,
			"timezone", berlinLocation,
		)
		go aiWorker.Run(ctx)
	}

	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return shutdownServers(shutdownContext, logger, httpServer, metricsServer)
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func injectedBuildInfo() (web.BuildInfo, error) {
	commit := strings.TrimSpace(buildCommit)
	timestamp := strings.TrimSpace(buildTime)
	if (commit == "" || commit == "dev") && (timestamp == "" || timestamp == "dev") {
		return web.BuildInfo{}, nil
	}
	if commit == "" || commit == "dev" || timestamp == "" || timestamp == "dev" {
		return web.BuildInfo{}, errors.New("build commit and build time must be provided together")
	}
	builtAt, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return web.BuildInfo{}, fmt.Errorf("parse build time: %w", err)
	}
	return web.BuildInfo{Commit: commit, BuiltAt: builtAt}, nil
}

func webProcessor(worker *processing.PipelineWorker) web.ProcessingRequester {
	if worker == nil {
		return nil
	}
	return worker
}

func restoreFeedSuccessMetric(ctx context.Context, database *store.Store, metrics *observability.Metrics) error {
	state, err := database.GetSyncState(ctx)
	if err != nil {
		return err
	}
	if state.LastSuccessAt != nil {
		metrics.SetLastFeedSuccess(*state.LastSuccessAt)
	}
	return nil
}

func runAIProcess(ctx context.Context, logger *slog.Logger, cfg config.Config, arguments []string) error {
	flags := flag.NewFlagSet("ai-process", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	incidentID := flags.Int64("incident", 0, "process one incident ID")
	all := flags.Bool("all", false, "process all unprocessed incidents")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse ai-process arguments: %w", err)
	}
	if (*incidentID > 0) == *all {
		return errors.New("ai-process requires exactly one of --incident ID or --all")
	}
	database, err := store.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer database.Close()
	models, err := database.PreferredPipelineModels(ctx, processing.StepKeys())
	if err != nil {
		return err
	}
	plans, err := processing.StepPlans(models)
	if err != nil {
		return err
	}
	var selectedID *int64
	if *incidentID > 0 {
		selectedID = incidentID
	}
	translationModel := ""
	if translationModels, translationErr := database.PreferredPipelineModels(ctx, []string{processing.TranslationModelStep}); translationErr == nil {
		translationModel = translationModels[processing.TranslationModelStep]
	}
	result, err := database.CreateManualPipelineCycle(ctx, cfg.SourceMode, plans, translationModel, selectedID, false, time.Now())
	if err != nil {
		return err
	}
	logger.Info("immediate AI processing requested",
		"requested", result.Requested,
		"cycle_id", result.CycleID,
	)
	return nil
}

func runMigrate(ctx context.Context, logger *slog.Logger, cfg config.Config) error {
	database, err := store.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.Ready(ctx); err != nil {
		return err
	}
	logger.Info("database migrations are current", "database", cfg.DatabasePath)
	return nil
}

func runOneShotSync(ctx context.Context, logger *slog.Logger, cfg config.Config) error {
	if cfg.SourceMode != "live" {
		return errors.New("one-shot sync requires MUNICHBRIEF_SOURCE_MODE=live")
	}
	database, err := store.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer database.Close()
	syncer, err := newLiveSyncer(database, cfg, nil, logger)
	if err != nil {
		return err
	}
	result, err := syncer.Sync(ctx)
	if err != nil {
		return err
	}
	logger.Info("one-shot synchronization completed",
		"not_modified", result.NotModified,
		"discovered", result.Discovered,
		"fetched", result.Fetched,
		"fetch_failures", result.FetchFailures,
		"parser_failures", result.ParserFailures,
		"skipped", result.Skipped,
	)
	return nil
}

func runBackup(ctx context.Context, cfg config.Config, arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	destination := flags.String("output", "", "backup destination, or - for stdout")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse backup arguments: %w", err)
	}
	if *destination == "" {
		return errors.New("backup requires --output PATH (or --output - for stdout)")
	}
	database, err := store.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer database.Close()
	if *destination != "-" {
		return database.Backup(ctx, *destination)
	}
	temporary, err := os.CreateTemp("", "munichbrief-backup-*.db")
	if err != nil {
		return fmt.Errorf("reserve temporary backup path: %w", err)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		return err
	}
	defer os.Remove(temporaryPath)
	if err := database.Backup(ctx, temporaryPath); err != nil {
		return err
	}
	backup, err := os.Open(temporaryPath)
	if err != nil {
		return fmt.Errorf("open temporary backup: %w", err)
	}
	defer backup.Close()
	if _, err := io.Copy(output, backup); err != nil {
		return fmt.Errorf("stream backup: %w", err)
	}
	return nil
}

func newLiveSyncer(database *store.Store, cfg config.Config, observer func(string, int), logger *slog.Logger) (*ingest.Syncer, error) {
	liveClient, err := source.NewHTTPClient(
		cfg.FeedURL,
		cfg.UserAgent,
		cfg.HTTPTimeout,
		1<<20,
		3<<20,
		nil,
	)
	if err != nil {
		return nil, err
	}
	liveClient.SetResponseObserver(observer)
	return ingest.NewSyncer(database, liveClient, cfg.RefreshAfter, time.Now, logger)
}

func configuredServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func serve(server *http.Server, component, address, sourceMode string, logger *slog.Logger, errorsChannel chan<- error) {
	logger.Info("munichbrief listener started", "component", component, "address", address, "source_mode", sourceMode)
	errorsChannel <- server.ListenAndServe()
}

func shutdownServers(ctx context.Context, logger *slog.Logger, servers ...*http.Server) error {
	var shutdownError error
	for _, server := range servers {
		if err := server.Shutdown(ctx); err != nil {
			shutdownError = errors.Join(shutdownError, err)
		}
	}
	logger.Info("munichbrief stopped")
	return shutdownError
}

func runLiveSyncLoop(ctx context.Context, location *time.Location, syncer *ingest.Syncer, metrics *observability.Metrics, logger *slog.Logger) {
	synchronize := func() bool {
		metrics.RecordFeedAttempt()
		startedAt := time.Now()
		result, err := syncer.Sync(ctx)
		metrics.RecordFeedDuration(time.Since(startedAt))
		if err != nil {
			metrics.RecordFeedFailure()
			if !errors.Is(err, context.Canceled) {
				logger.Error("live synchronization failed", "error", err)
			}
			return false
		}
		metrics.RecordFeedSuccess(result.NotModified, result.Discovered, result.Fetched, result.FetchFailures, result.ParserFailures, time.Now())
		logger.Info("live synchronization completed",
			"not_modified", result.NotModified,
			"discovered", result.Discovered,
			"fetched", result.Fetched,
			"fetch_failures", result.FetchFailures,
			"parser_failures", result.ParserFailures,
			"skipped", result.Skipped,
		)
		return true
	}

	succeeded := synchronize()
	failedAttempts := 0
	for {
		var delay time.Duration
		if succeeded {
			failedAttempts = 0
			delay = randomizedLiveSyncDelay(time.Duration(rand.Int64N(int64(2 * liveSyncJitter))))
		} else {
			failedAttempts++
			delay = liveSyncRetryDelay(failedAttempts)
		}
		nextRun := time.Now().Add(delay).In(location)
		metrics.SetNextFeedSync(nextRun)
		logger.Info("next live synchronization scheduled",
			"scheduled_at", nextRun,
			"retrying_after_failure", !succeeded,
			"failed_attempts", failedAttempts,
		)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			succeeded = synchronize()
		}
	}
}

func randomizedLiveSyncDelay(offset time.Duration) time.Duration {
	return liveSyncInterval - liveSyncJitter + offset
}

func liveSyncRetryDelay(failedAttempts int) time.Duration {
	delay := liveSyncRetryInitial
	for attempt := 1; attempt < failedAttempts && delay < liveSyncRetryMaximum; attempt++ {
		delay *= 2
	}
	return min(delay, liveSyncRetryMaximum)
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, "MunichBrief commands:")
	fmt.Fprintln(writer, "  munichbrief serve                 Run the reader and scheduler (default)")
	fmt.Fprintln(writer, "  munichbrief migrate               Apply and verify database migrations")
	fmt.Fprintln(writer, "  munichbrief sync                  Run one live synchronization")
	fmt.Fprintln(writer, "  munichbrief backup --output PATH  Create a consistent SQLite backup")
	fmt.Fprintln(writer, "  munichbrief backup --output -     Stream a consistent backup to stdout")
	fmt.Fprintln(writer, "  munichbrief ai-process --incident ID  Request immediate processing for one incident")
	fmt.Fprintln(writer, "  munichbrief ai-process --all          Request immediate processing for all unprocessed incidents")
}
