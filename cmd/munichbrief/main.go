package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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

var version = "dev"

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
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q; use munichbrief help", command)
	}
}

func runServer(ctx context.Context, logger *slog.Logger, cfg config.Config) error {
	database, err := store.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer database.Close()

	metrics := observability.NewMetrics(version, time.Now())
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
		liveSyncer, err = newLiveSyncer(database, cfg, metrics.ObserveSourceResponse)
		if err != nil {
			return err
		}
	}
	var aiWorker *processing.Worker
	if cfg.AIEnabled {
		ollamaClient, err := processing.NewOllamaClient(
			cfg.OllamaBaseURL,
			cfg.OllamaModel,
			cfg.AITimeout,
			cfg.AIContextSize,
			nil,
		)
		if err != nil {
			return err
		}
		aiWorker, err = processing.NewWorker(database, ollamaClient, metrics, logger, cfg.AIInterval, time.Now)
		if err != nil {
			return err
		}
	}

	webServer, err := web.New(database, logger, cfg.PageSize, cfg.SourceMode)
	if err != nil {
		return err
	}
	httpServer := configuredServer(cfg.Address, metrics.Wrap(webServer.Handler()))
	metricsServer := configuredServer(cfg.MetricsAddress, metrics.Handler())

	serveErrors := make(chan error, 2)
	go serve(httpServer, "reader", cfg.Address, cfg.SourceMode, logger, serveErrors)
	go serve(metricsServer, "metrics", cfg.MetricsAddress, cfg.SourceMode, logger, serveErrors)
	if liveSyncer != nil {
		go runLiveSyncLoop(ctx, cfg.SyncInterval, liveSyncer, metrics, logger)
	}
	if aiWorker != nil {
		logger.Info("AI processing worker started", "base_url", cfg.OllamaBaseURL, "model", cfg.OllamaModel)
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
	syncer, err := newLiveSyncer(database, cfg, nil)
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

func newLiveSyncer(database *store.Store, cfg config.Config, observer func(string, int)) (*ingest.Syncer, error) {
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
	return ingest.NewSyncer(database, liveClient, cfg.RefreshAfter, time.Now)
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

func runLiveSyncLoop(ctx context.Context, interval time.Duration, syncer *ingest.Syncer, metrics *observability.Metrics, logger *slog.Logger) {
	synchronize := func() {
		metrics.RecordFeedAttempt()
		result, err := syncer.Sync(ctx)
		if err != nil {
			metrics.RecordFeedFailure()
			if !errors.Is(err, context.Canceled) {
				logger.Error("live synchronization failed", "error", err)
			}
			return
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
	}

	synchronize()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			synchronize()
		}
	}
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, "MunichBrief commands:")
	fmt.Fprintln(writer, "  munichbrief serve                 Run the reader and scheduler (default)")
	fmt.Fprintln(writer, "  munichbrief migrate               Apply and verify database migrations")
	fmt.Fprintln(writer, "  munichbrief sync                  Run one live synchronization")
	fmt.Fprintln(writer, "  munichbrief backup --output PATH  Create a consistent SQLite backup")
	fmt.Fprintln(writer, "  munichbrief backup --output -     Stream a consistent backup to stdout")
}
