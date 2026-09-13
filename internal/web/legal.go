package web

import (
	_ "embed"
	"fmt"
	"net/http"
	"strings"
)

//go:embed templates/legal.html
var legalTemplate string

//go:embed templates/contact_admin.html
var contactAdminTemplate string

// Operator information is explicitly authorized for public display. The postal
// service address is neither a server location nor a residential address.
type operatorInformation struct{ Name, CareOf, Street, City, Country, Email string }

var publicOperator = operatorInformation{"Ege Kocabaş", "c/o COCENTER", "Koppoldstr. 1", "86551 Aichach", "Germany", "contact@munichbrief.de"}

type legalSection struct{ Title, Copy string }
type legalPage struct {
	basePage
	Title    string
	Operator operatorInformation
	Sections []legalSection
	Privacy  bool
}

func (s *Server) legal(w http.ResponseWriter, r *http.Request) {
	lang, ok := s.routeLanguage(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.setLanguagePreference(w, lang)
	privacy := strings.HasSuffix(r.URL.Path, "/privacy")
	title := "ImpressumTitle"
	if privacy {
		title = "PrivacyTitle"
	}
	base := s.base(r, lang, r.URL.Path)
	base.ShowAIDisclosure = false
	base.ShowReviewNotice = false
	base.SocialTitle = s.localization.Text(lang, title) + " · MunichBrief"
	base.Description = s.localization.Text(lang, title+"Description")
	base.StructuredData = structuredPageData(base, "WebPage")
	data := legalPage{basePage: base, Title: title, Operator: publicOperator, Privacy: privacy}
	if privacy {
		for _, key := range []string{"PrivacyController", "PrivacyHosting", "PrivacyCloudflare", "PrivacyPreferences", "PrivacyMonitoring", "PrivacyCorrespondence", "PrivacyRequired", "PrivacyRetention", "PrivacyPost", "PrivacySources", "PrivacySourcePeople", "PrivacyAutomation", "PrivacyRights", "PrivacyObjection"} {
			data.Sections = append(data.Sections, legalSection{key, key + "Copy"})
		}
	} else {
		data.Sections = []legalSection{{"ImpressumResponsible", "ImpressumResponsibleCopy"}, {"ContactHeading", "ImpressumContactCopy"}}
	}
	if wantsMarkdown(r.Header.Get("Accept")) {
		s.prepareMarkdown(w, base)
		writeMarkdownFrontMatterBuilder(w, data, s)
		return
	}
	s.prepareHTML(w, r, base)
	if err := s.legalTemplate.ExecuteTemplate(w, "layout", data); err != nil {
		s.logger.Error("render legal page failed")
	}
}
func writeMarkdownFrontMatterBuilder(w http.ResponseWriter, data legalPage, s *Server) {
	var b strings.Builder
	writeMarkdownFrontMatter(&b, s.localization.Text(data.Lang, data.Title), data.basePage)
	fmt.Fprintf(&b, "# %s\n\n%s\n\n", s.localization.Text(data.Lang, data.Title), data.Description)
	o := data.Operator
	fmt.Fprintf(&b, "%s  \n%s  \n%s  \n%s  \n%s  \n%s\n\n", o.Name, o.CareOf, o.Street, o.City, s.localization.Text(data.Lang, "LegalGermany"), o.Email)
	for _, section := range data.Sections {
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", s.localization.Text(data.Lang, section.Title), s.localization.Text(data.Lang, section.Copy))
	}
	fmt.Fprintf(&b, "[%s](/%s/contact#contact-form)\n\n", s.localization.Text(data.Lang, "ContactForm"), data.Lang)
	if data.Privacy {
		fmt.Fprintf(&b, "## %s\n\n", s.localization.Text(data.Lang, "PrivacyLinks"))
		b.WriteString(legalProviderMarkdown)
	}
	fmt.Fprint(w, b.String())
}

const legalProviderMarkdown = "[Cloudflare](https://www.cloudflare.com/privacypolicy/) · [Google](https://policies.google.com/privacy) · [SMTP2GO](https://www.smtp2go.com/privacy/) · [COCENTER / anschrift.net](https://anschrift.net/datenschutzerklaerung/) · [BayLDA](https://www.lda.bayern.de/de/beschwerde.html)\n"
