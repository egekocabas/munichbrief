package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/egekocabas/munichbrief/internal/config"
	"github.com/egekocabas/munichbrief/internal/processing"
	"github.com/egekocabas/munichbrief/internal/store"
)

func runLocationEvaluation(ctx context.Context, cfg config.Config, args []string, progress io.Writer) error {
	flags := flag.NewFlagSet("location-evaluate", flag.ContinueOnError)
	database := flags.String("snapshot", "", "Isolated copy of the database (migrations may run)")
	model := flags.String("model", "", "Installed evaluation model")
	output := flags.String("output", "", "New private JSONL result file")
	ids := flags.String("ids", "", "Optional comma-separated incident IDs")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *database == "" || *model == "" || *output == "" || flags.NArg() != 0 {
		return errors.New("location-evaluate requires --snapshot, --model, and --output")
	}
	if *database == cfg.DatabasePath {
		return errors.New("evaluation requires a separate snapshot path")
	}
	snapshotInfo, err := os.Stat(*database)
	if err != nil {
		return err
	}
	if !snapshotInfo.Mode().IsRegular() {
		return errors.New("snapshot must be a regular file")
	}
	if liveInfo, err := os.Stat(cfg.DatabasePath); err == nil && os.SameFile(liveInfo, snapshotInfo) {
		return errors.New("snapshot aliases configured live database")
	}

	selected := map[int64]bool{}
	if *ids != "" {
		for _, part := range strings.Split(*ids, ",") {
			id, err := strconv.ParseInt(part, 10, 64)
			if err != nil || id < 1 {
				return errors.New("invalid incident IDs")
			}
			selected[id] = true
		}
	}
	db, err := store.Open(ctx, *database)
	if err != nil {
		return err
	}
	defer db.Close()
	inputs, err := db.LocationEvaluationInputs(ctx)
	if err != nil {
		return err
	}
	client, err := processing.NewOllamaClient(cfg.OllamaBaseURL, *model, cfg.AITimeout, cfg.AIContextSize, nil)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, input := range inputs {
		if len(selected) > 0 && !selected[input.IncidentID] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		started := time.Now()
		value, identity, err := client.GenerateStep(ctx, processing.LocationVerificationDefinition(), processing.StepInput{Values: input.Values})
		result := map[string]any{"incident_id": input.IncidentID, "model": identity, "prompt_version": processing.LocationVerificationPromptVersion, "duration_seconds": time.Since(started).Seconds()}
		if err != nil {
			result["error"] = err.Error()
		} else {
			result["assessment"] = json.RawMessage(value.Values["location_assessment"])
		}
		if err := encoder.Encode(result); err != nil {
			return err
		}
		if err := file.Sync(); err != nil {
			return err
		}
		fmt.Fprintf(progress, "incident %d: completed (valid=%t)\n", input.IncidentID, err == nil)
	}
	return nil
}
