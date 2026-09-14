package web

import (
	_ "embed"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/egekocabas/munichbrief/internal/licensing"
)

//go:embed templates/licenses.html
var licensesTemplate string

type creditComponent struct {
	licensing.Component
	Texts        []licensing.Notice
	Llama        bool
	ModifiedIcon bool
}
type creditGroup struct {
	Heading    string
	Components []creditComponent
}
type creditsPage struct {
	basePage
	Groups                    []creditGroup
	UpdatedDate, UpdatedLabel string
}

func (s *Server) licenses(w http.ResponseWriter, r *http.Request) {
	lang, ok := s.routeLanguage(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.setLanguagePreference(w, lang)
	base := s.base(r, lang, "/"+lang+"/licenses")
	base.ShowAIDisclosure, base.ShowReviewNotice = false, false
	base.Description = s.localization.Text(lang, "LicensesDescription")
	base.SocialTitle = s.localization.Text(lang, "LicensesTitle") + " · MunichBrief"
	base.DocumentModifiedDate = licensing.CreditsUpdatedAt()
	base.StructuredData = structuredPageData(base, "WebPage")
	updated, _ := time.Parse(time.DateOnly, licensing.CreditsUpdatedAt())
	data := creditsPage{basePage: base, UpdatedDate: licensing.CreditsUpdatedAt(), UpdatedLabel: s.formatIncidentDate(lang, updated)}
	for _, kind := range []string{"project", "software", "font", "asset", "data", "model", "service"} {
		group := creditGroup{Heading: map[string]string{"project": "LicensesProject", "software": "LicensesSoftware", "font": "LicensesFonts", "asset": "LicensesAssets", "data": "LicensesData", "model": "LicensesModels", "service": "LicensesService"}[kind]}
		for _, c := range licensing.Components() {
			if c.Kind != kind || !c.Public {
				continue
			}
			item := creditComponent{Component: c, Llama: strings.Contains(c.License, "Llama-3"), ModifiedIcon: c.ID == "eu-ai-icons"}
			for _, id := range c.Notices {
				if n, ok := licensing.PublicNotice(id); ok {
					item.Texts = append(item.Texts, n)
				}
			}
			group.Components = append(group.Components, item)
		}
		if len(group.Components) > 0 {
			data.Groups = append(data.Groups, group)
		}
	}
	if wantsMarkdown(r.Header.Get("Accept")) {
		s.prepareMarkdown(w, base)
		var b strings.Builder
		writeMarkdownFrontMatter(&b, s.localization.Text(lang, "LicensesTitle"), base)
		fmt.Fprintf(&b, "# %s\n\n%s\n\n%s: %s\n\n%s\n\n%s\n\n", s.localization.Text(lang, "LicensesTitle"), base.Description, s.localization.Text(lang, "LegalLastUpdated"), data.UpdatedLabel, s.localization.Text(lang, "LicensesIntro"), s.localization.Text(lang, "LicensesOriginal"))
		for _, g := range data.Groups {
			fmt.Fprintf(&b, "## %s\n\n", s.localization.Text(lang, g.Heading))
			if g.Heading == "LicensesModels" {
				fmt.Fprintf(&b, "%s\n\n", s.localization.Text(lang, "LicensesModelIntro"))
			}
			if g.Heading == "LicensesData" {
				fmt.Fprintf(&b, "%s\n\n", s.localization.Text(lang, "LicensesDataChanges"))
			}
			for _, c := range g.Components {
				fmt.Fprintf(&b, "### %s — %s\n\n%s: %s\n\n[%s](%s)\n\n", c.Name, c.Version, s.localization.Text(lang, "LicensesLicense"), c.License, s.localization.Text(lang, "LicensesSource"), c.Source)
				if c.Llama {
					b.WriteString("Built with Meta Llama 3\n\n")
				}
				if c.ModifiedIcon {
					fmt.Fprintf(&b, "%s\n\n", s.localization.Text(lang, "LicensesIconChanges"))
				}
				for _, n := range c.Texts {
					fmt.Fprintf(&b, "[%s: %s](/%s/licenses/text/%s)\n\n```text\n%s\n```\n\n", s.localization.Text(lang, "LicensesDownload"), n.ID, lang, n.ID, n.Text)
				}
			}
		}
		fmt.Fprint(w, b.String())
		return
	}
	s.prepareHTML(w, r, base)
	if err := s.licensesTemplate.ExecuteTemplate(w, "layout", data); err != nil {
		s.logger.Error("render credits page failed")
	}
}

func (s *Server) licenseText(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.routeLanguage(r); !ok {
		http.NotFound(w, r)
		return
	}
	n, ok := licensing.PublicNotice(r.PathValue("notice"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.txt"`, n.ID))
	fmt.Fprint(w, n.Text)
}
