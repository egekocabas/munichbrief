package web

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

func TestContactMultipleTabsKeepIndependentSubmissions(t *testing.T) {
	s, db := newContactServer(t)
	first, firstToken := findContactForm(t, contactRequest(s, "GET", "/en/contact", nil))
	current, secondToken := findContactForm(t, contactRequest(s, "GET", "/en/contact", nil, first))
	if first.Value != current.Value || firstToken == secondToken {
		t.Fatal("tabs need a stable browser binding and different submission nonces")
	}
	for i, token := range []string{firstToken, secondToken, firstToken} {
		message := "First enquiry"
		if i == 1 {
			message = "Second enquiry"
		}
		form := url.Values{"token": {token}, "email": {"reader@example.org"}, "topic": {"general"}, "message": {message}}
		w := contactRequest(s, "POST", "/en/contact", form, current)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("submission %d: %d", i, w.Code)
		}
		for _, cookie := range w.Result().Cookies() {
			if cookie.Name == contactCookie && cookie.Value != current.Value {
				t.Fatal("receipt replaced the browser binding")
			}
		}
		// Following the confirmation redirect must not invalidate another open tab.
		current, _ = findContactForm(t, contactRequest(s, "GET", "/en/contact", nil, current))
	}
	rows, total, err := db.ListContacts(context.Background(), "", 1)
	if err != nil || total != 2 || rows[0].Message != "Second enquiry" || rows[1].Message != "First enquiry" {
		t.Fatalf("distinct submissions/retry: %+v total=%d err=%v", rows, total, err)
	}
}

func TestContactExpiredFormPreservesEscapedDraftAndCanRetry(t *testing.T) {
	s, db := newContactServer(t)
	before := time.Now().Add(-2 * time.Hour)
	expired := &http.Cookie{Name: contactCookie, Value: s.contactToken(before)}
	token := s.boundContactToken(before, "form:public:"+expired.Value+":")
	message := "Please correct <script>alert('test')</script> 中文"
	form := url.Values{"token": {token}, "email": {"reader@example.org"}, "topic": {"correction"}, "message": {message}}
	w := contactRequest(s, "POST", "/en/contact", form, expired)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), html.EscapeString(message)) || strings.Contains(w.Body.String(), "<script>alert(") {
		t.Fatalf("expired draft not safely preserved: %d", w.Code)
	}
	current, fresh := findContactForm(t, w)
	if current.Value == expired.Value || fresh == token || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("expired form did not get a private fresh retry")
	}
	_, count, err := db.ListContacts(context.Background(), "", 1)
	if err != nil || count != 0 {
		t.Fatal("expired request stored", count, err)
	}
	form.Set("token", fresh)
	if w = contactRequest(s, "POST", "/en/contact", form, current); w.Code != http.StatusSeeOther {
		t.Fatal(w.Code)
	}
}

func TestContactFormBindingAndOriginalCookieLifetime(t *testing.T) {
	s, _ := newContactServer(t)
	old := &http.Cookie{Name: contactCookie, Value: s.contactToken(time.Now().Add(-59 * time.Minute))}
	current, token := findContactForm(t, contactRequest(s, "GET", "/en/contact", nil, old))
	if current.Value != old.Value || current.MaxAge <= 0 || current.MaxAge > 60 {
		t.Fatalf("page load extended the security cookie: %+v", current)
	}
	other, _ := findContactForm(t, contactRequest(s, "GET", "/en/contact", nil))
	form := url.Values{"token": {token}, "email": {"reader@example.org"}, "topic": {"general"}, "message": {"Synthetic enquiry"}}
	if w := contactRequest(s, "POST", "/en/contact", form, other); w.Code != http.StatusForbidden {
		t.Fatal("accepted another browser's form", w.Code)
	}
	if w := contactRequest(s, "POST", "/en/contact", form); w.Code != http.StatusForbidden {
		t.Fatal("accepted missing cookie", w.Code)
	}
	// Even with the same browser binding, a public form cannot authorize admin actions.
	form = url.Values{"token": {token}, "control": {"form"}, "enabled": {"false"}}
	if w := contactAdminRequest(s, "POST", "/admin/contact/settings", form, current); w.Code != http.StatusForbidden {
		t.Fatal("accepted public token for admin", w.Code)
	}
}

func TestContactAdminMultipleTabsAndMissingEmailKey(t *testing.T) {
	s, db := newContactServer(t)
	s.options.ContactNotificationsConfigured = false
	first, firstToken := findContactForm(t, contactAdminRequest(s, "GET", "/admin/contact", nil))
	secondPage := contactAdminRequest(s, "GET", "/admin/contact", nil, first)
	current, secondToken := findContactForm(t, secondPage)
	if current.Value != first.Value || secondToken == firstToken {
		t.Fatal("admin forms do not coexist")
	}
	_, control, found := strings.Cut(secondPage.Body.String(), `aria-label="Send email notifications"`)
	if !found {
		t.Fatal("missing email switch")
	}
	control, _, _ = strings.Cut(control, "</button>")
	if !strings.Contains(control, `aria-checked="true"`) || strings.Contains(control, "disabled") || !strings.Contains(control, ">On") {
		t.Fatal("saved On must remain visible and allow disabling without a key", control)
	}
	form := url.Values{"token": {firstToken}, "control": {"notifications"}, "enabled": {"false"}}
	if w := contactAdminRequest(s, "POST", "/admin/contact/settings", form, current); w.Code != http.StatusSeeOther {
		t.Fatal("first tab action", w.Code)
	}
	// Another admin page and another action must not invalidate the second tab.
	current, _ = findContactForm(t, contactAdminRequest(s, "GET", "/admin/contact", nil, current))
	form.Set("token", secondToken)
	form.Set("control", "form")
	if w := contactAdminRequest(s, "POST", "/admin/contact/settings", form, current); w.Code != http.StatusSeeOther {
		t.Fatal("second tab action", w.Code)
	}
	form.Set("control", "notifications")
	form.Set("enabled", "true")
	if w := contactAdminRequest(s, "POST", "/admin/contact/settings", form, current); w.Code != http.StatusConflict {
		t.Fatal("enabled sending without a key", w.Code)
	}
	settings, err := db.ContactSettings(context.Background())
	if err != nil || settings.NotificationsEnabled {
		t.Fatal("Off not saved", settings, err)
	}
	s.options.ContactNotificationsConfigured = true
	if _, err = db.ClaimContact(context.Background(), time.Now(), 20, 300); !errors.Is(err, store.ErrContactPaused) {
		t.Fatal("restoring credentials resumed disabled sending", err)
	}
}
