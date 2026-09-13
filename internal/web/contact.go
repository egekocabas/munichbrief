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
	Enabled, Received                   bool
	Token, Email, Topic, Message, Error string
	Errors                              map[string]string
}

func (s *Server) contactToken(now time.Time) string {
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ""
	}
	payload := strconv.FormatInt(now.Add(time.Hour).Unix(), 10) + "." + hex.EncodeToString(nonce[:])
	return payload + "." + s.contactMAC(payload)
}
func (s *Server) contactMAC(v string) string {
	mac := hmac.New(sha256.New, []byte(s.options.ContactSecret))
	mac.Write([]byte(v))
	return hex.EncodeToString(mac.Sum(nil))
}
func (s *Server) validContactToken(v string, now time.Time) bool {
	parts := strings.Split(v, ".")
	if len(parts) != 3 || len(v) > 180 {
		return false
	}
	expires, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || expires <= now.Unix() || expires > now.Add(time.Hour).Unix() {
		return false
	}
	return hmac.Equal([]byte(parts[2]), []byte(s.contactMAC(parts[0]+"."+parts[1])))
}
func (s *Server) setContactCookie(w http.ResponseWriter, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: contactCookie, Value: value, Path: "/", MaxAge: age, HttpOnly: true, Secure: s.options.SecureCookies, SameSite: http.SameSiteLaxMode})
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
			return false
		}
		v.Until = now.Add(15 * time.Minute)
	}
	v.Count++
	s.contactLimits[key] = v
	return v.Count <= 3
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
	if data.Received || status != 200 {
		base.Robots = "noindex,follow"
	}
	base.StructuredData = structuredPageData(base, "ContactPage")
	data.basePage = base
	data.Enabled = s.options.ContactEnabled
	if data.Errors == nil {
		data.Errors = map[string]string{}
	}
	if data.Token == "" && data.Enabled {
		data.Token = s.contactToken(time.Now())
		s.setContactCookie(w, data.Token, 3600)
	}
	if wantsMarkdown(r.Header.Get("Accept")) {
		s.prepareMarkdown(w, base)
		w.WriteHeader(status)
		fmt.Fprintf(w, "# %s\n\n%s\n\n%s\n\ncontact@munichbrief.de\n\n", s.localization.Text(language, "ContactHeading"), base.Description, s.localization.Text(language, "ContactEnglish"))
		for _, key := range []string{"ContactCorrectionCopy", "ContactPrivacyCopy", "ContactTechnicalCopy", "ContactPoliceCopy", "ContactStorage"} {
			fmt.Fprintf(w, "%s\n\n", s.localization.Text(language, key))
		}
		fmt.Fprintf(w, "[%s](/%s/contact#contact-form) · [%s](/%s/privacy) · [%s](/%s/impressum)\n", s.localization.Text(language, "ContactForm"), language, s.localization.Text(language, "PrivacyTitle"), language, s.localization.Text(language, "ImpressumTitle"), language)
		return
	}
	s.prepareHTML(w, r, base)
	w.WriteHeader(status)
	if err := s.contactTemplate.ExecuteTemplate(w, "layout", data); err != nil {
		s.logger.Error("render contact page failed")
	}
}
func (s *Server) submitContact(w http.ResponseWriter, r *http.Request) {
	if !s.options.ContactEnabled {
		http.NotFound(w, r)
		return
	}
	language, _ := s.routeLanguage(r)
	data := contactPage{}
	invalid := func(status int, key string) { data.Error = key; s.renderContact(w, r, data, status) }
	if !validReaderMutation(r) || !isFormPost(r) {
		invalid(403, "ContactInvalid")
		return
	}
	if !s.allowContact(r, time.Now()) {
		w.Header().Set("Retry-After", "900")
		invalid(429, "ContactRateLimit")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	if err := r.ParseForm(); err != nil {
		invalid(400, "ContactInvalid")
		return
	}
	token := r.PostForm.Get("token")
	cookie, err := r.Cookie(contactCookie)
	if err != nil || !hmac.Equal([]byte(cookie.Value), []byte(token)) || !s.validContactToken(token, time.Now()) || r.PostForm.Get("website") != "" {
		invalid(403, "ContactInvalid")
		return
	}
	data.Token = token
	data.Email = strings.TrimSpace(r.PostForm.Get("email"))
	data.Topic = r.PostForm.Get("topic")
	data.Message = strings.TrimSpace(r.PostForm.Get("message"))
	sum := sha256.Sum256([]byte(token))
	message := store.ContactMessage{SubmissionHash: hex.EncodeToString(sum[:]), Email: data.Email, Topic: data.Topic, Message: data.Message, Language: language, CreatedAt: time.Now().Unix()}
	if err := message.Validate(); err != nil {
		key := map[string]string{"email": "ContactEmailInvalid", "topic": "ContactTopicInvalid", "message": "ContactMessageInvalid"}[err.Error()]
		if key == "" {
			key = "ContactInvalid"
		}
		data.Errors = map[string]string{err.Error(): key}
		invalid(400, "ContactInvalid")
		return
	}
	created, err := s.contactStore.CreateContact(r.Context(), message)
	if err != nil {
		invalid(503, "ContactSaveFailed")
		return
	}
	if created {
		s.options.ContactMetrics.Received.Add(1)
	}
	// Keep the original token for a short time so browser retries remain idempotent.
	s.setContactCookie(w, token, 3600)
	http.SetCookie(w, &http.Cookie{Name: "munichbrief_contact_received", Value: s.contactToken(time.Now()), Path: "/", MaxAge: 300, HttpOnly: true, Secure: s.options.SecureCookies, SameSite: http.SameSiteLaxMode})
	w.Header().Set("Cache-Control", "private, no-store")
	http.Redirect(w, r, "/"+language+"/contact#contact-form", http.StatusSeeOther)
}

type contactAdminPage struct {
	Messages                             []store.ContactMessage
	Detail                               *store.ContactMessage
	Token, Filter, Previous, Next, Error string
	Total                                int
	Paused                               bool
	Now                                  int64
	Stats                                store.ContactStats
}

func (s *Server) contactAdmin(w http.ResponseWriter, r *http.Request) {
	s.renderContactAdmin(w, r, "", 200)
}
func (s *Server) renderContactAdmin(w http.ResponseWriter, r *http.Request, errorText string, status int) {
	if s.contactStore == nil || len(s.options.ContactSecret) < 32 {
		http.NotFound(w, r)
		return
	}
	page := contactAdminPage{Token: s.contactToken(time.Now()), Filter: r.URL.Query().Get("filter"), Error: errorText, Paused: !s.options.ContactNotificationsConfigured || s.options.ContactMetrics.Paused.Load(), Now: time.Now().Unix()}
	s.setContactCookie(w, page.Token, 3600)
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
	if s.contactStore == nil || len(s.options.ContactSecret) < 32 {
		http.NotFound(w, r)
		return
	}
	if !validReaderMutation(r) || !isFormPost(r) {
		http.Error(w, "Invalid request", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid request", 400)
		return
	}
	cookie, err := r.Cookie(contactCookie)
	token := r.PostForm.Get("token")
	if err != nil || !hmac.Equal([]byte(cookie.Value), []byte(token)) || !s.validContactToken(token, time.Now()) {
		http.Error(w, "Expired form; reload and try again", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	action := r.PostForm.Get("action")
	if action == "delete" && r.PostForm.Get("confirm") != "yes" {
		s.renderContactAdmin(w, r, "Confirm permanent deletion before continuing.", 400)
		return
	}
	review, _ := time.Parse("2006-01-02", r.PostForm.Get("review_date"))
	if err := s.contactStore.UpdateContact(r.Context(), id, action, r.PostForm.Get("reason"), review.Unix(), time.Now().Unix()); err != nil {
		s.renderContactAdmin(w, r, "Action unavailable. Check the hold dates and notification status, then try again.", 400)
		return
	}
	target := "/admin/contact/" + strconv.FormatInt(id, 10)
	if action == "delete" {
		target = "/admin/contact"
	}
	w.Header().Set("Cache-Control", "private, no-store")
	http.Redirect(w, r, target, http.StatusSeeOther)
}
