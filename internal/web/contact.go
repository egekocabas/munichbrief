package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

const contactCookie = "munichbrief_contact"

type contactRate struct {
	Until time.Time
	Count int
}
type contactPage struct {
	basePage
	UpdatedLabel                        string
	Enabled, Received, Draft            bool
	Token, Email, Topic, Message, Error string
	Errors                              map[string]string
}

func (s *Server) contactToken(now time.Time) string {
	return s.boundContactToken(now, "")
}
func (s *Server) boundContactToken(now time.Time, binding string) string {
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ""
	}
	payload := strconv.FormatInt(now.Add(time.Hour).Unix(), 10) + "." + hex.EncodeToString(nonce[:])
	return payload + "." + s.contactMAC(binding+payload)
}
func (s *Server) contactMAC(v string) string {
	mac := hmac.New(sha256.New, []byte(s.options.ContactSecret))
	mac.Write([]byte(v))
	return hex.EncodeToString(mac.Sum(nil))
}
func (s *Server) validContactToken(v string, now time.Time) bool {
	return s.validBoundContactToken(v, now, "")
}
func (s *Server) validBoundContactToken(v string, now time.Time, binding string) bool {
	parts := strings.Split(v, ".")
	if len(parts) != 3 || len(v) > 180 {
		return false
	}
	expires, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || expires <= now.Unix() || expires > now.Add(time.Hour).Unix() {
		return false
	}
	return hmac.Equal([]byte(parts[2]), []byte(s.contactMAC(binding+parts[0]+"."+parts[1])))
}
func (s *Server) setContactCookie(w http.ResponseWriter, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: contactCookie, Value: value, Path: "/", MaxAge: age, HttpOnly: true, Secure: s.options.SecureCookies, SameSite: http.SameSiteLaxMode})
}

// Keep a browser binding stable for its original one-hour lifetime. Each form
// gets its own signed nonce, so tabs coexist without sharing an idempotency key.
func (s *Server) contactFormToken(w http.ResponseWriter, r *http.Request, kind string) string {
	now := time.Now()
	cookie, err := r.Cookie(contactCookie)
	if err != nil || !s.validContactToken(cookie.Value, now) {
		cookie = &http.Cookie{Value: s.contactToken(now)}
	}
	if cookie.Value == "" {
		return ""
	}
	expires, _ := strconv.ParseInt(strings.Split(cookie.Value, ".")[0], 10, 64)
	s.setContactCookie(w, cookie.Value, int(expires-now.Unix()))
	return s.boundContactToken(now, "form:"+kind+":"+cookie.Value+":")
}
func (s *Server) validContactForm(r *http.Request, token, kind string, now time.Time) bool {
	cookie, err := r.Cookie(contactCookie)
	return err == nil && s.validContactToken(cookie.Value, now) &&
		s.validBoundContactToken(token, now, "form:"+kind+":"+cookie.Value+":")
}

func (s *Server) contactClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown"
	}
	peer = peer.Unmap()
	trusted := func(ip netip.Addr) bool {
		for _, prefix := range s.options.ContactTrustedProxies {
			if prefix.Contains(ip) {
				return true
			}
		}
		return false
	}
	if trusted(peer) {
		// Walk the configured proxy chain from the socket backwards. Never trust an
		// arbitrary browser-supplied CF-Connecting-IP or leftmost forwarding value.
		chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for i := len(chain) - 1; i >= 0 && trusted(peer); i-- {
			candidate, e := netip.ParseAddr(strings.TrimSpace(chain[i]))
			if e != nil {
				break
			}
			peer = candidate.Unmap()
		}
	}
	return peer.String()
}
func (s *Server) allowContact(r *http.Request, now time.Time) bool {
	key := s.contactMAC("rate:" + s.contactClientIP(r))
	s.contactMu.Lock()
	defer s.contactMu.Unlock()
	for k, v := range s.contactLimits {
		if !now.Before(v.Until) {
			delete(s.contactLimits, k)
		}
	}
	v, exists := s.contactLimits[key]
	if !exists {
		if len(s.contactLimits) >= 10000 {
			s.contactBlocked++
			return false
		}
		v.Until = now.Add(15 * time.Minute)
	}
	v.Count++
	s.contactLimits[key] = v
	if v.Count <= 3 {
		s.contactAllowed++
		return true
	}
	s.contactBlocked++
	return false
}
func (s *Server) renderContact(w http.ResponseWriter, r *http.Request, data contactPage, status int) {
	language, ok := s.routeLanguage(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.setLanguagePreference(w, language)
	base := s.base(r, language, "/"+language+"/contact")
	base.ShowAIDisclosure = false
	base.ShowReviewNotice = false
	base.Description = s.localization.Text(language, "ContactIntro")
	base.SocialTitle = s.localization.Text(language, "ContactHeading") + " · MunichBrief"
	if data.Received || status != http.StatusOK {
		base.Robots = "noindex,follow"
	}
	base.DocumentModifiedDate = informationPageUpdatedAt("contact").Format(time.DateOnly)
	base.StructuredData = structuredPageData(base, "ContactPage")
	data.UpdatedLabel = s.formatIncidentDate(language, informationPageUpdatedAt("contact"))
	data.basePage = base
	data.Enabled = s.contactAvailable
	if data.Enabled {
		settings, err := s.contactStore.ContactSettings(r.Context())
		if err != nil {
			data.Enabled = false
			data.Error = "ContactSaveFailed"
			status = http.StatusServiceUnavailable
			base.Robots = "noindex,follow"
			data.basePage = base
		} else {
			data.Enabled = settings.FormEnabled
		}
	}
	data.Draft = !data.Enabled && (data.Email != "" || data.Message != "")
	w.Header().Set("Cache-Control", "private, no-store")
	if data.Errors == nil {
		data.Errors = map[string]string{}
	}
	if data.Token == "" && data.Enabled {
		data.Token = s.contactFormToken(w, r, "public")
	}
	if wantsMarkdown(r.Header.Get("Accept")) {
		s.prepareMarkdown(w, base)
		w.Header().Set("Cache-Control", "private, no-store")
		w.WriteHeader(status)
		fmt.Fprintf(w, "# %s\n\n%s\n\n%s: %s\n\ncontact@munichbrief.de\n\n", s.localization.Text(language, "ContactHeading"), base.Description, s.localization.Text(language, "LegalLastUpdated"), data.UpdatedLabel)
		for _, key := range []string{"ContactCorrectionCopy", "ContactPrivacyCopy", "ContactTechnicalCopy", "ContactPoliceCopy", "ContactStorage"} {
			if key == "ContactPoliceCopy" {
				fmt.Fprintf(w, "%s [%s](https://kontakte.polizei.bayern.de/)\n\n", markdownText(s.localization.Text(language, key)), markdownText(s.localization.Text(language, "ContactPoliceLink")))
			} else {
				fmt.Fprintf(w, "%s\n\n", s.localization.Text(language, key))
			}
		}
		if data.Error != "" {
			fmt.Fprintf(w, "%s\n\n", s.localization.Text(language, data.Error))
		}
		if !data.Enabled {
			fmt.Fprintf(w, "%s\n\n", s.localization.Text(language, "ContactUnavailable"))
		}
		fmt.Fprintf(w, "[%s](/%s/contact#contact-form) · [%s](/%s/privacy) · [%s](/%s/impressum)\n", s.localization.Text(language, "ContactForm"), language, s.localization.Text(language, "PrivacyTitle"), language, s.localization.Text(language, "ImpressumTitle"), language)
		return
	}
	s.prepareHTML(w, r, base)
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	if err := s.contactTemplate.ExecuteTemplate(w, "layout", data); err != nil {
		s.logger.Error("render contact page failed")
	}
}
func (s *Server) submitContact(w http.ResponseWriter, r *http.Request) {

	language, _ := s.routeLanguage(r)
	data := contactPage{}
	invalid := func(status int, key string) { data.Error = key; s.renderContact(w, r, data, status) }
	if !validReaderMutation(r) || !isFormPost(r) {
		invalid(http.StatusForbidden, "ContactInvalid")
		return
	}
	allowed := s.allowContact(r, time.Now())
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	if err := r.ParseForm(); err != nil {
		invalid(http.StatusBadRequest, "ContactInvalid")
		return
	}
	// Preserve bounded, escaped draft fields when an expired form or rate limit
	// requires another attempt. Nothing is stored until all checks pass.
	data.Email = strings.TrimSpace(r.PostForm.Get("email"))
	data.Topic = r.PostForm.Get("topic")
	data.Message = strings.TrimSpace(r.PostForm.Get("message"))
	token := r.PostForm.Get("token")
	validToken := s.validContactForm(r, token, "public", time.Now())
	if validToken {
		data.Token = token // Keep the original idempotency key on a rate-limited retry.
	}
	if !allowed {
		w.Header().Set("Retry-After", "900")
		invalid(http.StatusTooManyRequests, "ContactRateLimit")
		return
	}
	if !validToken || r.PostForm.Get("website") != "" {
		invalid(http.StatusForbidden, "ContactInvalid")
		return
	}
	if !s.contactAvailable {
		invalid(http.StatusServiceUnavailable, "ContactClosed")
		return
	}
	sum := sha256.Sum256([]byte(token))
	message := store.ContactMessage{SubmissionHash: hex.EncodeToString(sum[:]), Email: data.Email, Topic: data.Topic, Message: data.Message, Language: language, CreatedAt: time.Now().Unix()}
	if err := message.Validate(); err != nil {
		key := map[string]string{"email": "ContactEmailInvalid", "topic": "ContactTopicInvalid", "message": "ContactMessageInvalid"}[err.Error()]
		if key == "" {
			key = "ContactInvalid"
		}
		data.Errors = map[string]string{err.Error(): key}
		invalid(http.StatusBadRequest, "ContactInvalid")
		return
	}
	created, err := s.contactStore.CreateContact(r.Context(), message)
	if errors.Is(err, store.ErrContactClosed) {
		invalid(http.StatusServiceUnavailable, "ContactClosed")
		return
	}
	if err != nil {
		invalid(http.StatusServiceUnavailable, "ContactSaveFailed")
		return
	}
	if created {
		s.options.ContactMetrics.Received.Add(1)
	}
	// Retain the browser binding: retries use the original form nonce.
	http.SetCookie(w, &http.Cookie{Name: "munichbrief_contact_received", Value: s.contactToken(time.Now()), Path: "/", MaxAge: 300, HttpOnly: true, Secure: s.options.SecureCookies, SameSite: http.SameSiteLaxMode})
	w.Header().Set("Cache-Control", "private, no-store")
	http.Redirect(w, r, "/"+language+"/contact#contact-form", http.StatusSeeOther)
}

type contactAdminPage struct {
	Messages                                                []store.ContactMessage
	Detail                                                  *store.ContactMessage
	Token, Filter, Previous, Next, Error                    string
	Total                                                   int
	Paused                                                  bool
	Now                                                     int64
	Stats                                                   store.ContactStats
	Settings                                                store.ContactSettings
	Budgets                                                 []store.ContactBudget
	ContactConfigured, NotificationsConfigured, TestEnabled bool
	Status                                                  string
	Rate                                                    contactRateStats
}

func (s *Server) contactAdmin(w http.ResponseWriter, r *http.Request) {
	s.renderContactAdmin(w, r, "", http.StatusOK)
}
func (s *Server) renderContactAdmin(w http.ResponseWriter, r *http.Request, errorText string, status int) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if s.contactStore == nil || len(s.options.ContactSecret) < 32 {
		http.NotFound(w, r)
		return
	}
	page := contactAdminPage{Token: s.contactFormToken(w, r, "admin"), Filter: r.URL.Query().Get("filter"), Error: errorText, Now: time.Now().Unix()}
	page.ContactConfigured = s.contactAvailable
	page.NotificationsConfigured = s.options.ContactNotificationsConfigured && s.contactAvailable
	page.Rate = s.contactRateStats(time.Now())
	var settingsErr error
	page.Settings, settingsErr = s.contactStore.ContactSettings(r.Context())
	if settingsErr == nil {
		page.Budgets, settingsErr = s.contactStore.ContactBudgets(r.Context(), time.Now(), s.options.ContactDailyLimit, s.options.ContactMonthlyLimit)
	}
	if settingsErr != nil {
		http.Error(w, "Inbox temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	page.Paused = !page.NotificationsConfigured || !page.Settings.NotificationsEnabled
	page.TestEnabled = page.ContactConfigured && page.Settings.FormEnabled && page.Settings.NotificationsEnabled && page.NotificationsConfigured
	for _, b := range page.Budgets {
		if b.Remaining == 0 {
			page.Paused = true
			page.TestEnabled = false
		}
	}
	switch r.URL.Query().Get("saved") {
	case "form":
		page.Status = "Form availability updated."
	case "notifications":
		page.Status = "Email notification setting updated."
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	var statsErr error
	page.Stats, statsErr = s.contactStore.ContactStats(r.Context())
	if statsErr != nil {
		http.Error(w, "Inbox temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	if raw := r.PathValue("id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		m, err := s.contactStore.Contact(r.Context(), id)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			http.Error(w, "Inbox temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		if err != nil {
			http.NotFound(w, r)
			return
		}
		page.Detail = &m
	} else {
		n := 1
		if raw := r.URL.Query().Get("page"); raw != "" {
			n, _ = strconv.Atoi(raw)
		}
		if n < 1 || n > 1000000 || (page.Filter != "" && page.Filter != "unread" && page.Filter != "unresolved" && page.Filter != "problems") {
			http.Error(w, "Invalid inbox query", http.StatusBadRequest)
			return
		}
		var err error
		page.Messages, page.Total, err = s.contactStore.ListContacts(r.Context(), page.Filter, n)
		if err != nil {
			http.Error(w, "Inbox temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		link := func(number int) string {
			return "/admin/contact?" + url.Values{"page": {strconv.Itoa(number)}, "filter": {page.Filter}}.Encode()
		}
		if n > 1 {
			page.Previous = link(n - 1)
		}
		if n*20 < page.Total {
			page.Next = link(n + 1)
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.contactAdminTemplate.ExecuteTemplate(w, "contact_admin", page); err != nil {
		s.logger.Error("render contact inbox failed")
	}
}
func (s *Server) contactAdminMutation(w http.ResponseWriter, r *http.Request) {
	if !s.validContactAdminPost(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	action := r.PostForm.Get("action")
	if action == "retry" && (!s.contactAvailable || !s.options.ContactNotificationsConfigured) {
		s.renderContactAdmin(w, r, "Email notifications are unavailable in the deployment configuration.", http.StatusConflict)
		return
	}
	if action == "delete" && r.PostForm.Get("confirm") != "yes" {
		s.renderContactAdmin(w, r, "Confirm permanent deletion before continuing.", http.StatusBadRequest)
		return
	}
	review, _ := time.Parse("2006-01-02", r.PostForm.Get("review_date"))
	if err := s.contactStore.UpdateContact(r.Context(), id, action, r.PostForm.Get("reason"), review.Unix(), time.Now().Unix()); err != nil {
		s.renderContactAdmin(w, r, "Action unavailable. Check the hold dates and notification status, then try again.", http.StatusBadRequest)
		return
	}
	target := "/admin/contact/" + strconv.FormatInt(id, 10)
	if action == "delete" {
		target = "/admin/contact"
	}
	w.Header().Set("Cache-Control", "private, no-store")
	http.Redirect(w, r, target, http.StatusSeeOther)
}
