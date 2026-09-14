package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSaveFeedbackUsesExplicitServerOutcomes(t *testing.T) {
	t.Run("HTMX emits a settled success event with the canonical message", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/settings/appearance", nil)
		req.Header.Set("HX-Request", "true")

		saveFeedback(rec, req, "Appearance saved.", saveFeedbackSuccess)

		got := rec.Header().Get("X-Save-Feedback")
		for _, want := range []string{`"save-feedback"`, `"message":"Saved"`, `"kind":"success"`} {
			if !strings.Contains(got, want) {
				t.Errorf("HTMX feedback header = %q, want %q", got, want)
			}
		}
		if strings.Contains(got, "Appearance") {
			t.Errorf("HTMX feedback header must carry the canonical copy only, got %q", got)
		}
		if cookie := rec.Header().Get("Set-Cookie"); cookie != "" {
			t.Errorf("HTMX feedback must not set a replayable redirect cookie, got %q", cookie)
		}
	})

	t.Run("HTMX keeps the workflow publish message verbatim", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/categories/1/workflow", nil)
		req.Header.Set("HX-Request", "true")

		saveFeedback(rec, req, saveFeedbackPublished, saveFeedbackSuccess)

		if got := rec.Header().Get("X-Save-Feedback"); !strings.Contains(got, `"message":"Published"`) {
			t.Errorf("publish feedback header = %q, want the verbatim Published copy", got)
		}
	})

	t.Run("native redirect carries one signed feedback record with the canonical message", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/settings/appearance", nil)

		saveFeedback(rec, req, "Appearance saved.", saveFeedbackSuccess)

		cookie := rec.Result().Cookies()
		if len(cookie) != 1 || cookie[0].Name != saveFeedbackCookie {
			t.Fatalf("native feedback cookie = %#v, want one %q cookie", cookie, saveFeedbackCookie)
		}
		if strings.Contains(cookie[0].Value, "Appearance") {
			t.Errorf("feedback cookie must not expose the message, got %q", cookie[0].Value)
		}

		followUp := httptest.NewRequest(http.MethodGet, "/settings", nil)
		followUp.AddCookie(cookie[0])
		got := readSaveFeedback(followUp)
		if got.Message != saveFeedbackSaved || got.Kind != saveFeedbackSuccess {
			t.Errorf("readSaveFeedback() = %#v, want signed success with the canonical copy", got)
		}
	})

	t.Run("drawer feedback targets the active drawer", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/settings/desks/1", nil)
		req.Header.Set("HX-Request", "true")

		saveDrawerFeedback(rec, req, saveFeedbackSaved)

		got := rec.Header().Get("X-Save-Feedback")
		for _, want := range []string{`"message":"Saved"`, `"kind":"success"`, `"target":"drawer"`} {
			if !strings.Contains(got, want) {
				t.Errorf("drawer feedback header = %q, want %q", got, want)
			}
		}
	})

	t.Run("consumed native feedback is cleared", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/settings", nil)
		req.AddCookie(&http.Cookie{Name: saveFeedbackCookie, Value: "signed-feedback"})
		rec := httptest.NewRecorder()

		clearSaveFeedbackCookie(rec, req)

		cookies := rec.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != saveFeedbackCookie || cookies[0].MaxAge != -1 {
			t.Fatalf("cleared feedback cookie = %#v, want one expired %q cookie", cookies, saveFeedbackCookie)
		}
	})
}

func TestSaveFeedbackRejectsForgedOrExpiredCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(&http.Cookie{Name: saveFeedbackCookie, Value: "forged"})

	if got := readSaveFeedback(req); got.Message != "" {
		t.Errorf("forged cookie produced feedback %#v", got)
	}
}

func TestSaveFeedbackRejectsExpiredCookie(t *testing.T) {
	now := time.Now()
	originalNow := saveFeedbackNow
	saveFeedbackNow = func() time.Time { return now }
	t.Cleanup(func() { saveFeedbackNow = originalNow })

	rec := httptest.NewRecorder()
	setSaveFeedbackCookie(rec, saveFeedbackData{Message: "Appearance saved.", Kind: saveFeedbackSuccess})
	cookie := rec.Result().Cookies()[0]
	saveFeedbackNow = func() time.Time { return now.Add(saveFeedbackTTL) }

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(cookie)
	if got := readSaveFeedback(req); got.Message != "" {
		t.Errorf("expired cookie produced feedback %#v", got)
	}
}
