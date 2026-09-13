package web

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func contactAdminRequest(s *Server, method, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://review.example"+path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://review.example")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func TestContactClosingStaleFormKeepsDraftAndAcceptedRetry(t *testing.T) {
	s, db := newContactServer(t)
	ctx := context.Background()
	first := findContactCookie(t, contactRequest(s, "GET", "/en/contact", nil))
	stale := findContactCookie(t, contactRequest(s, "GET", "/en/contact", nil))
	form := url.Values{"token": {first.Value}, "email": {"reader@example.org"}, "topic": {"general"}, "message": {"Hello <script>bad()</script>"}}
	if w := contactRequest(s, "POST", "/en/contact", form, first); w.Code != 303 {
		t.Fatal(w.Code)
	}
	if err := db.SetContactControl(ctx, "form", false); err != nil {
		t.Fatal(err)
	}
	if w := contactRequest(s, "POST", "/en/contact", form, first); w.Code != 303 {
		t.Fatal("accepted retry rejected", w.Code)
	}
	form.Set("token", stale.Value)
	w := contactRequest(s, "POST", "/en/contact", form, stale)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "Your message was not sent") || !strings.Contains(w.Body.String(), "Hello &lt;script&gt;") || strings.Contains(w.Body.String(), "type=\"submit\"") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(w.Body.String(), "noindex") || !strings.Contains(w.Body.String(), "readonly") {
		t.Fatal("draft safety", w.Body.String())
	}
	_, count, err := db.ListContacts(ctx, "", 1)
	if err != nil || count != 1 {
		t.Fatal(count, err)
	}
	get := contactRequest(s, "GET", "/en/contact", nil)
	if get.Code != 200 || strings.Contains(get.Body.String(), "name=\"message\"") || !strings.Contains(get.Body.String(), "form is not currently available") {
		t.Fatal(get.Code, get.Body.String())
	}
	if err = db.SetContactControl(ctx, "form", true); err != nil {
		t.Fatal(err)
	}
	get = contactRequest(s, "GET", "/en/contact", nil)
	if !strings.Contains(get.Body.String(), "name=\"message\"") {
		t.Fatal("form did not reopen")
	}
}
func TestContactAdminControlsQuotasTestAndGuards(t *testing.T) {
	s, db := newContactServer(t)
	s.options.ContactNotificationsConfigured = true
	get := contactAdminRequest(s, "GET", "/admin/contact", nil)
	if get.Code != 200 {
		t.Fatal(get.Code, get.Body.String())
	}
	for _, text := range []string{"Contact controls", "Email budget", "of 20 attempts remaining", "of 300 attempts remaining", "Send test email", "Active limit windows"} {
		if !strings.Contains(get.Body.String(), text) {
			t.Fatal("missing", text)
		}
	}
	if strings.Index(get.Body.String(), ">Messages</a>") > strings.Index(get.Body.String(), ">Open reader</a>") {
		t.Fatal("nav order")
	}
	c := findContactCookie(t, get)
	form := url.Values{"token": {c.Value}, "control": {"notifications"}, "enabled": {"false"}}
	if w := contactAdminRequest(s, "POST", "/admin/contact/settings", form, c); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := contactAdminRequest(s, "POST", "/admin/contact/test", url.Values{"token": {c.Value}}, c); w.Code != 409 {
		t.Fatal("disabled test", w.Code)
	}
	// Error pages refresh the cookie; obtain a fresh form before continuing.
	c = findContactCookie(t, contactAdminRequest(s, "GET", "/admin/contact", nil))
	form.Set("token", c.Value)
	form.Set("enabled", "true")
	if w := contactAdminRequest(s, "POST", "/admin/contact/settings", form, c); w.Code != 303 {
		t.Fatal(w.Code)
	}
	testForm := url.Values{"token": {c.Value}}
	var target string
	for range 2 {
		w := contactAdminRequest(s, "POST", "/admin/contact/test", testForm, c)
		if w.Code != 303 {
			t.Fatal(w.Code, w.Body.String())
		}
		target = w.Header().Get("Location")
	}
	rows, count, err := db.ListContacts(context.Background(), "", 1)
	if err != nil || count != 1 || !rows[0].IsTest {
		t.Fatal(rows, count, err)
	}
	detail := contactAdminRequest(s, "GET", target, nil)
	if !strings.Contains(detail.Body.String(), "Test email #") || !strings.Contains(detail.Body.String(), "pending") {
		t.Fatal(detail.Body.String())
	}
	for _, path := range []string{"/admin/contact/settings", "/admin/contact/test"} {
		if w := contactRequest(s, "POST", path, testForm, c); w.Code != 404 {
			t.Fatal("public mutation", w.Code)
		}
		if w := contactAdminRequest(s, "POST", path, url.Values{"token": {"bad"}}, c); w.Code != 403 || w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("CSRF", w.Code)
		}
		r := httptest.NewRequest("POST", "https://review.example"+path, strings.NewReader(testForm.Encode()))
		r.Header.Set("Origin", "https://evil.example")
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(c)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("cross-origin", w.Code)
		}
	}
	s.options.ContactEnabled = false
	if w := contactAdminRequest(s, "POST", "/admin/contact/test", testForm, c); w.Code != 409 {
		t.Fatal("deployment gate", w.Code)
	}
	db.Close()
	if w := contactAdminRequest(s, "GET", "/admin/contact", nil); w.Code != 503 {
		t.Fatal("database error", w.Code)
	}
}
func TestContactRateStatisticsDoNotExposeIdentifiers(t *testing.T) {
	s, _ := newContactServer(t)
	now := time.Now()
	r := httptest.NewRequest("POST", "/en/contact", nil)
	r.RemoteAddr = "192.0.2.55:1234"
	for range 4 {
		s.allowContact(r, now)
	}
	v := s.contactRateStats(now)
	if v.Active != 1 || v.Allowed != 3 || v.Blocked != 1 {
		t.Fatal(v)
	}
	v = s.contactRateStats(now.Add(15 * time.Minute))
	if v.Active != 0 {
		t.Fatal("counted expired window")
	}
	if len(s.contactLimits) != 1 {
		t.Fatal("view unexpectedly changed cleanup semantics")
	}
	body := contactAdminRequest(s, "GET", "/admin/contact", nil).Body.String()
	if strings.Contains(body, "192.0.2.55") || strings.Contains(body, s.contactMAC("rate:192.0.2.55")) {
		t.Fatal("exposed identifier")
	}
	if !s.allowContact(r, now.Add(15*time.Minute)) {
		t.Fatal("window did not reset")
	}
}

func TestContactInboxOnlyReceiptAndLocaleParity(t *testing.T) {
	s, db := newContactServer(t)
	if err := db.SetContactControl(context.Background(), "notifications", false); err != nil {
		t.Fatal(err)
	}
	c := findContactCookie(t, contactRequest(s, "GET", "/en/contact", nil))
	form := url.Values{"token": {c.Value}, "email": {"reader@example.org"}, "topic": {"general"}, "message": {"Inbox-only enquiry"}}
	if w := contactRequest(s, "POST", "/en/contact", form, c); w.Code != 303 {
		t.Fatal(w.Code)
	}
	rows, count, err := db.ListContacts(context.Background(), "", 1)
	if err != nil || count != 1 || rows[0].NotificationCode != "notifications_disabled" {
		t.Fatal(rows, err)
	}
	for _, language := range s.languages {
		for _, key := range []string{"ContactClosed", "ContactKeepDraft"} {
			if text := s.localization.Text(language.Code, key); text == "" || strings.Contains(text, "["+key+"]") {
				t.Fatal("missing translation", language.Code, key)
			}
		}
		for _, accept := range []string{"text/html", "text/markdown"} {
			r := httptest.NewRequest("GET", "https://munichbrief.de/"+language.Code+"/privacy", nil)
			r.Header.Set("Accept", accept)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			expected := s.localization.Text(language.Code, "PrivacyMonitoringCopy")
			if accept == "text/html" {
				expected = html.EscapeString(expected)
			}
			if w.Code != 200 || !strings.Contains(w.Body.String(), expected) {
				t.Fatal("privacy copy missing", language.Code, accept, w.Code)
			}
		}
	}
}
