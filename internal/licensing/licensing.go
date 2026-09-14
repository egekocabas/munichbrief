// Package licensing exposes the reviewed, build-time licence inventory.
// It never contacts providers or changes model eligibility.
package licensing

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"slices"
)

//go:embed bundle.json
var bundleJSON []byte

type Component struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Version          string   `json:"version"`
	Source           string   `json:"source"`
	License          string   `json:"license"`
	Notices          []string `json:"notices"`
	Kind             string   `json:"kind"`
	Locations        []string `json:"locations"`
	Public           bool     `json:"public"`
	Conditions       string   `json:"conditions"`
	ReviewedAt       string   `json:"reviewed_at"`
	ReviewStatus     string   `json:"review_status"`
	ModelTag         string   `json:"model_tag"`
	Digest           string   `json:"digest"`
	UpstreamRevision string   `json:"upstream_revision"`
	ArtifactSource   string   `json:"artifact_source"`
	ArtifactRevision string   `json:"artifact_revision"`
	Quantization     string   `json:"quantization"`
}

type Notice struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Source string `json:"source"`
}

type Inventory struct {
	CreditsUpdatedAt string      `json:"credits_updated_at"`
	ReviewedAt       string      `json:"reviewed_at"`
	Components       []Component `json:"components"`
	Notices          []Notice    `json:"notices"`
}

var inventory = load()

func load() Inventory {
	var result Inventory
	if err := json.Unmarshal(bundleJSON, &result); err != nil {
		panic(fmt.Sprintf("invalid embedded licence inventory: %v", err))
	}
	return result
}

func ReviewedAt() string { return inventory.ReviewedAt }

// CreditsUpdatedAt is maintained separately from individual component reviews.
func CreditsUpdatedAt() string { return inventory.CreditsUpdatedAt }

// Components returns independent slices so callers cannot change the registry.
func Components() []Component {
	result := slices.Clone(inventory.Components)
	for i := range result {
		result[i].Notices = slices.Clone(result[i].Notices)
		result[i].Locations = slices.Clone(result[i].Locations)
	}
	return result
}

// PublicNotice resolves only notice IDs attached to public credits. It does not
// interpret file names or filesystem paths supplied by a request.
func PublicNotice(id string) (Notice, bool) {
	for _, c := range inventory.Components {
		if c.Public && slices.Contains(c.Notices, id) {
			for _, n := range inventory.Notices {
				if n.ID == id {
					return n, true
				}
			}
		}
	}
	return Notice{}, false
}

type Review struct {
	Component Component
	Label     string
	Warning   bool
}

func Assess(tag, digest string, available bool) Review {
	r := Review{Label: "Review needed", Warning: true}
	for _, c := range inventory.Components {
		if c.ModelTag == tag && tag != "" {
			r.Component = c
			break
		}
	}
	if !available {
		r.Label = "Catalogue unavailable"
		return r
	}
	if r.Component.ModelTag == "" || digest == "" {
		return r
	}
	if digest != r.Component.Digest {
		r.Label = "Artefact changed"
		return r
	}
	switch r.Component.ReviewStatus {
	case "reviewed":
		r.Label, r.Warning = "Reviewed", false
	case "conditions":
		r.Label = "Conditions apply"
	}
	return r
}

// WriteNotices works before application configuration or database initialization.
func WriteNotices(w io.Writer) error {
	if _, err := fmt.Fprintln(w, "MunichBrief — licences and third-party notices"); err != nil {
		return err
	}
	for _, c := range inventory.Components {
		if _, err := fmt.Fprintf(w, "\n%s (%s)\n%s\nLicence: %s\nNotice IDs: %v\n", c.Name, c.Version, c.Source, c.License, c.Notices); err != nil {
			return err
		}
	}
	for _, n := range inventory.Notices {
		if _, err := fmt.Fprintf(w, "\n--- %s ---\nSource: %s\n\n%s\n", n.ID, n.Source, n.Text); err != nil {
			return err
		}
	}
	return nil
}
