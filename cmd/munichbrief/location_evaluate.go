package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
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
	livePath, err := evaluationLiveDatabasePath(cfg.DatabasePath)
	if err != nil {
		return err
	}
	if liveInfo, err := os.Stat(livePath); err == nil && os.SameFile(liveInfo, snapshotInfo) {
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
	inputs, err = selectLocationEvaluationInputs(inputs, selected)
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

// SQLite accepts file: URIs as well as filesystem paths. Compare their actual
// files before store.Open can migrate a supposed evaluation snapshot.
func evaluationLiveDatabasePath(path string) (string, error) {
	if !strings.HasPrefix(path, "file:") {
		return path, nil
	}
	u, err := url.Parse(path)
	if err != nil || (u.Host != "" && u.Host != "localhost") {
		return "", errors.New("cannot establish isolation from configured database URI")
	}
	if u.Opaque != "" {
		decoded, err := url.PathUnescape(u.Opaque)
		if err != nil {
			return "", err
		}
		return decoded, nil
	}
	return u.Path, nil
}

// Validate the complete requested cohort before creating output or contacting a
// provider. Silently dropping missing IDs would bias the evaluation results.
func selectLocationEvaluationInputs(inputs []store.LocationEvaluationInput, selected map[int64]bool) ([]store.LocationEvaluationInput, error) {
	var result []store.LocationEvaluationInput
	found := make(map[int64]bool)
	for _, input := range inputs {
		if len(selected) == 0 || selected[input.IncidentID] {
			result = append(result, input)
			found[input.IncidentID] = true
		}
	}
	var missing []int64
	for id := range selected {
		if !found[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
		return nil, fmt.Errorf("no eligible current presentation for incident IDs %v", missing)
	}
	if len(result) == 0 {
		return nil, errors.New("no eligible current presentations in snapshot")
	}
	return result, nil
}
