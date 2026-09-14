package web

import (
	_ "embed"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/egekocabas/munichbrief/internal/licensing"
	"github.com/egekocabas/munichbrief/internal/processing"
)

//go:embed templates/licenses_admin.html
var licensesAdminTemplate string

type modelLicenseRow struct {
	Tag, Digest, Uses string
	Available         bool
	Review            licensing.Review
}
type adminLicensesPage struct {
	Rows      []modelLicenseRow
	Available bool
	Updated   string
}

func modelLicenseRows(models processing.PipelineModelStatus) []modelLicenseRow {
	uses := map[string][]string{}
	for _, step := range models.Steps {
		if step.Preferred != "" {
			uses[step.Preferred] = append(uses[step.Preferred], step.DisplayName)
		}
	}
	for _, p := range models.PostProcessors {
		if !p.PerScopeSettings && p.Preferred != "" {
			uses[p.Preferred] = append(uses[p.Preferred], p.DisplayName)
		}
		for _, scope := range p.Scopes {
			if scope.PreferredModel != "" {
				uses[scope.PreferredModel] = append(uses[scope.PreferredModel], p.DisplayName+" / "+scope.DisplayName)
			}
		}
	}
	tags := map[string]bool{}
	for _, tag := range models.Models {
		tags[tag] = true
	}
	for tag := range uses {
		if _, ok := tags[tag]; !ok {
			tags[tag] = false
		}
	}
	var rows []modelLicenseRow
	for tag, installed := range tags {
		r := licensing.Assess(tag, models.ModelDigests[tag], models.CatalogAvailable)
		if !installed && models.CatalogAvailable {
			r.Label, r.Warning = "Review needed", true
		}
		rows = append(rows, modelLicenseRow{Tag: tag, Digest: models.ModelDigests[tag], Uses: strings.Join(uses[tag], ", "), Available: installed, Review: r})
	}
	sort.Slice(rows, func(i, j int) bool {
		if (rows[i].Uses != "") != (rows[j].Uses != "") {
			return rows[i].Uses != ""
		}
		if rows[i].Review.Warning != rows[j].Review.Warning {
			return rows[i].Review.Warning
		}
		return rows[i].Tag < rows[j].Tag
	})
	return rows
}

func modelLicenseWarning(models processing.PipelineModelStatus, tag string) string {
	if tag == "" {
		return ""
	}
	if models.CatalogAvailable && !slices.Contains(models.Models, tag) {
		return "Review needed"
	}
	r := licensing.Assess(tag, models.ModelDigests[tag], models.CatalogAvailable)
	if r.Warning {
		return r.Label
	}
	return ""
}

func (s *Server) adminLicenses(w http.ResponseWriter, r *http.Request) {
	models := processing.PipelineModelStatus{}
	if s.options.Processor != nil {
		var err error
		models, err = s.options.Processor.ModelStatus(r.Context())
		if err != nil {
			s.internalError(w, r, "read model licence status", err)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if err := s.licensesAdminTemplate.ExecuteTemplate(w, "licenses_admin", adminLicensesPage{Rows: modelLicenseRows(models), Available: models.CatalogAvailable, Updated: licensing.ReviewedAt()}); err != nil {
		s.logger.Error("render model licence status failed")
	}
}
