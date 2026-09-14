package web

import (
	"context"
	"fmt"
	"github.com/egekocabas/munichbrief/internal/store"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newContactServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	db := fixtureStore(t)
	for _, control := range []string{"form", "notifications"} {
		if err := db.SetContactControl(context.Background(), control, true); err != nil {
			t.Fatal(err)
		}
	}
	s, e := NewWithOptions(db, slog.Default(), Options{PageSize: 20, SourceMode: "fixture", PresentationMode: "public", AdminEnabled: true, PublicHosts: []string{"munichbrief.de"}, CanonicalOrigin: "https://munichbrief.de", ContactEnabled: true, ContactSecret: strings.Repeat("s", 32), SecureCookies: true})
	if e != nil {
		t.Fatal(e)
	}
	return s, db
}
func contactRequest(s *Server, method, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://munichbrief.de"+path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://munichbrief.de")
	r.RemoteAddr = "192.0.2.1:1234"
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func findContactCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == contactCookie {
			return c
		}
	}
	t.Fatal("missing contact cookie")
	return nil
}
func findContactForm(t *testing.T, w *httptest.ResponseRecorder) (*http.Cookie, string) {
	t.Helper()
	cookie := findContactCookie(t, w)
	_, rest, ok := strings.Cut(w.Body.String(), `name="token" value="`)
	if !ok {
		t.Fatal("missing form token")
	}
	token, _, ok := strings.Cut(rest, `"`)
	if !ok || token == "" {
		t.Fatal("empty form token")
	}
	return cookie, html.UnescapeString(token)
}
func TestContactSSRReceiptDuplicateAndPrivacy(t *testing.T) {
	s, db := newContactServer(t)
	get := contactRequest(s, "GET", "/tr/contact", nil)
	if get.Code != 200 || !strings.Contains(get.Body.String(), "İngilizce") {
		t.Fatal(get.Code)
	}
	c, token := findContactForm(t, get)
	if !c.HttpOnly || !c.Secure || c.MaxAge != 3600 {
		t.Fatal(c)
	}
	f := url.Values{"token": {token}, "email": {"reader@example.org"}, "topic": {"correction"}, "message": {"请更正 İstanbul <script>bad()</script>"}}
	for range 2 {
		w := contactRequest(s, "POST", "/tr/contact", f, c)
		if w.Code != 303 || w.Header().Get("Location") != "/tr/contact#contact-form" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	rows, count, e := db.ListContacts(context.Background(), "", 1)
	if e != nil || count != 1 || rows[0].NotificationState != "pending" {
		t.Fatal(e, count)
	}
}
func TestContactValidationAndAbuse(t *testing.T) {
	for _, tc := range []struct {
		name, field, value string
		status             int
	}{{"email", "email", "x\r\nBcc: victim@example.org", 400}, {"single-label email", "email", "asdasd@asdasda", 400}, {"invalid domain label", "email", "reader@-example.org", 400}, {"topic", "topic", "unknown", 400}, {"large unicode", "message", strings.Repeat("好", 5001), 400}, {"honeypot", "website", "spam", 403}, {"token", "token", "forged", 403}} {
		t.Run(tc.name, func(t *testing.T) {
			s, db := newContactServer(t)
			c, token := findContactForm(t, contactRequest(s, "GET", "/en/contact", nil))
			f := url.Values{"token": {token}, "email": {"reader@example.org"}, "topic": {"general"}, "message": {"Hello"}}
			f.Set(tc.field, tc.value)
			w := contactRequest(s, "POST", "/en/contact", f, c)
			if w.Code != tc.status {
				t.Fatal(w.Code, w.Body.String())
			}
			_, n, _ := db.ListContacts(context.Background(), "", 1)
			if n != 0 {
				t.Fatal("stored invalid submission")
			}
		})
	}
	s, db := newContactServer(t)
	c, token := findContactForm(t, contactRequest(s, "GET", "/en/contact", nil))
	f := url.Values{"token": {token}, "email": {"reader@example.org"}, "topic": {"general"}, "message": {"Hi"}}
	for i := 0; i < 4; i++ {
		w := contactRequest(s, "POST", "/en/contact", f, c)
		if i == 3 {
			if w.Code != 429 {
				t.Fatal("not rate limited", w.Code)
			}
			if !strings.Contains(w.Body.String(), `name="token" value="`+token+`"`) || !strings.Contains(w.Body.String(), `>Hi</textarea>`) {
				t.Fatal("rate limit discarded the draft or original idempotency key")
			}
		}
	}
	r := httptest.NewRequest("POST", "https://munichbrief.de/en/contact", strings.NewReader(f.Encode()))
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(c)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	db.Close()
	r = httptest.NewRequest("POST", "https://munichbrief.de/en/contact", strings.NewReader(f.Encode()))
	r.Header.Set("Origin", "https://munichbrief.de")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "192.0.2.2:1234"
	r.AddCookie(c)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 503 || strings.Contains(w.Body.String(), "Your message has been received") {
		t.Fatal("database failure acknowledged", w.Code)
	}
}
func TestContactAdminBoundaryEscapingAndRetentionActions(t *testing.T) {
	s, db := newContactServer(t)
	_, e := db.CreateContact(context.Background(), store.ContactMessage{SubmissionHash: fmt.Sprintf("%064d", 1), Email: "reader@example.org", Topic: "general", Message: "<script>alert('bad')</script>", Language: "en", CreatedAt: time.Now().Unix()})
	if e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/admin/contact", "/admin/contact/1"} {
		if w := contactRequest(s, "GET", path, nil); w.Code != 404 {
			t.Fatal("public admin access", w.Code)
		}
	}
	admin := func(method, path string, form url.Values, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://review.example"+path, strings.NewReader(form.Encode()))
		r.Header.Set("Origin", "https://review.example")
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	w := admin("GET", "/admin/contact/1", nil, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "<script>alert(") || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Fatal(w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("X-Robots-Tag"), "noindex") {
		t.Fatal("indexable inbox")
	}
	c, token := findContactForm(t, w)
	f := url.Values{"token": {token}, "action": {"resolve"}}
	if w = admin("POST", "/admin/contact/1", f, c); w.Code != 303 {
		t.Fatal(w.Code)
	}
	m, _ := db.Contact(context.Background(), 1)
	if m.ResolvedAt == 0 || m.NotificationState != "cancelled" {
		t.Fatal(m)
	}
	f.Set("action", "delete")
	if w = admin("POST", "/admin/contact/1", f, c); w.Code != 400 {
		t.Fatal("unconfirmed deletion")
	}
}
func TestContactTokensAndTrustedProxy(t *testing.T) {
	s, _ := newContactServer(t)
	now := time.Now()
	if s.validContactToken(s.contactToken(now.Add(-2*time.Hour)), now) {
		t.Fatal("expired token")
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.0.2.1:123"
	r.Header.Set("X-Forwarded-For", "203.0.113.4")
	r.Header.Set("CF-Connecting-IP", "203.0.113.5")
	if s.contactClientIP(r) != "192.0.2.1" {
		t.Fatal("trusted spoofed header")
	}
	s.options.ContactTrustedProxies = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	r.Header.Set("X-Forwarded-For", "198.51.100.7, 192.0.2.2")
	if s.contactClientIP(r) != "198.51.100.7" {
		t.Fatal(s.contactClientIP(r))
	}
}
func TestLegalPagesEveryLanguageHTMLAndMarkdown(t *testing.T) {
	s, _ := newContactServer(t)
	for _, lang := range s.languages {
		for _, path := range []string{"privacy", "impressum", "contact"} {
			r := httptest.NewRequest("GET", "https://munichbrief.de/"+lang.Code+"/"+path, nil)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("%s/%s %d", lang.Code, path, w.Code)
			}
			for _, key := range []string{"PrivacyTitle", "ImpressumTitle", "ContactHeading"} {
				if strings.Contains(w.Body.String(), "["+key+"]") {
					t.Fatal("missing locale", lang.Code, key)
				}
			}
			if path != "contact" {
				if !strings.Contains(w.Body.String(), "Ege Kocabaş") || !strings.Contains(w.Body.String(), "86551 Aichach") {
					t.Fatal("missing operator", lang.Code)
				}
			}
			r.Header.Set("Accept", "text/markdown")
			mw := httptest.NewRecorder()
			s.Handler().ServeHTTP(mw, r)
			if mw.Code != 200 || strings.Contains(mw.Body.String(), "[ContactEmergencyCopy]") {
				t.Fatal("markdown", lang.Code)
			}
			if path == "contact" {
				if !strings.Contains(w.Body.String(), `href="https://kontakte.polizei.bayern.de/"`) || !strings.Contains(mw.Body.String(), "](https://kontakte.polizei.bayern.de/)") {
					t.Errorf("%s: missing official police contact link in HTML or Markdown", lang.Code)
				}
			}
			if path != "contact" {
				date := legalPageUpdatedAt(path)
				iso := date.Format(time.DateOnly)
				label := s.formatIncidentDate(lang.Code, date)
				if !strings.Contains(w.Body.String(), `<time datetime="`+iso+`">`+html.EscapeString(label)+`</time>`) || !strings.Contains(mw.Body.String(), s.localization.Text(lang.Code, "LegalLastUpdated")+": "+label) {
					t.Errorf("%s/%s: missing localized revision date", lang.Code, path)
				}
				if !strings.Contains(w.Body.String(), `"dateModified":"`+iso+`"`) || strings.Contains(w.Body.String(), `property="article:modified_time"`) {
					t.Errorf("%s/%s: incorrect document revision metadata", lang.Code, path)
				}
			}
			if path == "impressum" {
				copy := s.localization.Text(lang.Code, "ImpressumRequestsCopy")
				if !strings.Contains(w.Body.String(), html.EscapeString(copy)) || !strings.Contains(mw.Body.String(), copy) {
					t.Errorf("%s: editorial request guidance missing", lang.Code)
				}
			}
			if path == "privacy" {
				for _, key := range []string{"PrivacyRequired", "PrivacySourcePeople", "PrivacyAutomation", "PrivacyObjection"} {
					for _, suffix := range []string{"", "Copy"} {
						text := s.localization.Text(lang.Code, key+suffix)
						if text == "" || strings.Contains(text, "["+key) || !strings.Contains(w.Body.String(), html.EscapeString(text)) || !strings.Contains(mw.Body.String(), text) {
							t.Errorf("%s: privacy disclosure %s missing from HTML or Markdown", lang.Code, key+suffix)
						}
					}
				}
			}
			if path != "contact" && (!strings.Contains(mw.Body.String(), "Ege Kocabaş") || !strings.Contains(mw.Body.String(), s.localization.Text(lang.Code, map[string]string{"privacy": "PrivacyRetentionCopy", "impressum": "ImpressumResponsibleCopy"}[path]))) {
				t.Fatal("markdown parity", lang.Code, path)
			}
		}
	}
}
