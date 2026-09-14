package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/egekocabas/munichbrief/internal/store"
)

type contactRateStats struct {
	Active           int
	Allowed, Blocked uint64
}

func (s *Server) contactRateStats(now time.Time) contactRateStats {
	s.contactMu.Lock()
	defer s.contactMu.Unlock()
	v := contactRateStats{Allowed: s.contactAllowed, Blocked: s.contactBlocked}
	// Counting must not extend retention or expose individual identifiers.
	for _, limit := range s.contactLimits {
		if now.Before(limit.Until) {
			v.Active++
		}
	}
	return v
}
func (s *Server) validContactAdminPost(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if s.contactStore == nil || len(s.options.ContactSecret) < 32 {
		http.NotFound(w, r)
		return false
	}
	if !validReaderMutation(r) || !isFormPost(r) {
		http.Error(w, "Invalid request", http.StatusForbidden)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return false
	}
	cookie, err := r.Cookie(contactCookie)
	token := r.PostForm.Get("token")
	if err != nil || !hmac.Equal([]byte(cookie.Value), []byte(token)) || !s.validContactToken(token, time.Now()) {
		http.Error(w, "Expired form; reload and try again", http.StatusForbidden)
		return false
	}
	return true
}
func (s *Server) contactAdminSettings(w http.ResponseWriter, r *http.Request) {
	if !s.validContactAdminPost(w, r) {
		return
	}
	control, value := r.PostForm.Get("control"), r.PostForm.Get("enabled")
	if (control != "form" && control != "notifications") || (value != "true" && value != "false") {
		s.renderContactAdmin(w, r, "Invalid contact setting.", http.StatusBadRequest)
		return
	}
	if value == "true" && (!s.contactAvailable || (control == "notifications" && !s.options.ContactNotificationsConfigured)) {
		s.renderContactAdmin(w, r, "Configure protected administration, the contact signing secret and, for sending, an SMTP2GO key first.", http.StatusConflict)
		return
	}
	if err := s.contactStore.SetContactControl(r.Context(), control, value == "true"); err != nil {
		s.renderContactAdmin(w, r, "The setting could not be saved. Please try again.", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, "/admin/contact?saved="+control+"#contact-controls", http.StatusSeeOther)
}
func (s *Server) contactAdminTest(w http.ResponseWriter, r *http.Request) {
	if !s.validContactAdminPost(w, r) {
		return
	}
	if !s.contactAvailable || !s.options.ContactNotificationsConfigured {
		s.renderContactAdmin(w, r, "Test email is unavailable in the deployment configuration.", http.StatusConflict)
		return
	}
	hash := sha256.Sum256([]byte("admin-test:" + r.PostForm.Get("token")))
	id, err := s.contactStore.QueueContactTest(r.Context(), hex.EncodeToString(hash[:]), time.Now(), s.options.ContactDailyLimit, s.options.ContactMonthlyLimit)
	if err != nil {
		text, status := "Could not queue the test email. Please try again.", http.StatusServiceUnavailable
		switch {
		case errors.Is(err, store.ErrContactClosed), errors.Is(err, store.ErrContactPaused):
			text, status = "Enable both the contact form and email notifications before sending a test.", http.StatusConflict
		case errors.Is(err, store.ErrContactBudget):
			text, status = "The notification budget is exhausted. Wait for the next budget reset.", http.StatusConflict
		case errors.Is(err, store.ErrContactTestPending):
			text, status = "A test email is already queued or sending. Check its entry in the inbox.", http.StatusConflict
		}
		s.renderContactAdmin(w, r, text, status)
		return
	}
	http.Redirect(w, r, "/admin/contact/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func contactNotificationLabel(m store.ContactMessage) string {
	if m.NotificationState == "accepted" {
		return "accepted by SMTP2GO"
	}
	if m.NotificationCode == "notifications_disabled" {
		return "inbox only — email was disabled at receipt"
	}
	if m.NotificationCode == "test_disabled" {
		return "cancelled — a contact control was disabled"
	}
	return m.NotificationState
}
