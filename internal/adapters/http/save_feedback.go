package httpadapter

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/giulianotesta7/tkt/internal/i18n"
)

const (
	saveFeedbackCookie = "tkt_save_feedback"
	saveFeedbackTTL    = time.Minute
)

// Canonical server-issued success copy: every persisted mutation reports
// exactly Saved, and a successful workflow publish is the sole exception.
// Failure feedback uses the dedicated in-page error paths, never this channel.
// The constants are the canonical English identity canonicalSaveFeedback
// enforces; the copy a user reads is resolved through the catalogue at delivery.
const (
	saveFeedbackSaved     = "Saved"
	saveFeedbackPublished = "Published"
)

// saveFeedbackKey maps the two canonical outcomes to their catalogue entries.
var saveFeedbackMessageKey = map[string]string{
	saveFeedbackSaved:     "feedback.saved",
	saveFeedbackPublished: "feedback.published",
}

// localizedSaveFeedback resolves the canonical English outcome into the
// request's interface language. Only the emitted copy changes; the constants
// stay the identity the invariant below enforces.
func localizedSaveFeedback(r *http.Request, data saveFeedbackData) saveFeedbackData {
	if key, ok := saveFeedbackMessageKey[data.Message]; ok {
		data.Message = i18n.T(requestLocale(r), key)
	}
	return data
}

// canonicalSaveFeedback enforces the copy invariant on both delivery channels
// (HTMX header and native flash cookie), so an action-specific string at a
// call site can never reach production feedback.
func canonicalSaveFeedback(data saveFeedbackData) saveFeedbackData {
	if data.Kind == saveFeedbackSuccess && data.Message != saveFeedbackPublished {
		data.Message = saveFeedbackSaved
	}
	return data
}

type saveFeedbackKind string

const saveFeedbackSuccess saveFeedbackKind = "success"

type saveFeedbackData struct {
	Message  string           `json:"message"`
	Kind     saveFeedbackKind `json:"kind"`
	Target   string           `json:"target,omitempty"`
	IssuedAt int64            `json:"issued_at,omitempty"`
}

var (
	saveFeedbackKey     [32]byte
	saveFeedbackKeyOnce sync.Once
	saveFeedbackNow     = time.Now
)

func saveFeedbackCipher() cipher.AEAD {
	saveFeedbackKeyOnce.Do(func() {
		if _, err := rand.Read(saveFeedbackKey[:]); err != nil {
			panic("httpadapter: generate save feedback key: " + err.Error())
		}
	})
	block, err := aes.NewCipher(saveFeedbackKey[:])
	if err != nil {
		panic("httpadapter: create save feedback cipher: " + err.Error())
	}
	cipher, err := cipher.NewGCM(block)
	if err != nil {
		panic("httpadapter: create save feedback GCM: " + err.Error())
	}
	return cipher
}

// saveFeedback emits a server-issued mutation outcome. HTMX carries it in a
// dedicated response header that the client reads after a settled swap. Native redirects carry an authenticated, short-lived flash record that the next renderer consumes once.
func saveFeedback(w http.ResponseWriter, r *http.Request, message string, kind saveFeedbackKind) {
	data := canonicalSaveFeedback(saveFeedbackData{Message: message, Kind: kind})
	if r.Header.Get("HX-Request") != "" {
		payload, err := json.Marshal(map[string]saveFeedbackData{"save-feedback": localizedSaveFeedback(r, data)})
		if err != nil {
			return
		}
		w.Header().Set("X-Save-Feedback", string(payload))
		return
	}
	setSaveFeedbackCookie(w, r, data)
}

func saveDrawerFeedback(w http.ResponseWriter, r *http.Request, message string) {
	data := canonicalSaveFeedback(saveFeedbackData{Message: message, Kind: saveFeedbackSuccess, Target: "drawer"})
	if r.Header.Get("HX-Request") != "" {
		payload, err := json.Marshal(map[string]saveFeedbackData{"save-feedback": localizedSaveFeedback(r, data)})
		if err == nil {
			w.Header().Set("X-Save-Feedback", string(payload))
		}
		return
	}
	setSaveFeedbackCookie(w, r, data)
}

func setSaveFeedbackCookie(w http.ResponseWriter, r *http.Request, data saveFeedbackData) {
	data = localizedSaveFeedback(r, canonicalSaveFeedback(data))
	data.IssuedAt = saveFeedbackNow().Unix()
	plain, err := json.Marshal(data)
	if err != nil {
		return
	}
	cipher := saveFeedbackCipher()
	nonce := make([]byte, cipher.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return
	}
	sealed := cipher.Seal(nonce, nonce, plain, nil)
	http.SetCookie(w, &http.Cookie{
		Name:     saveFeedbackCookie,
		Value:    base64.RawURLEncoding.EncodeToString(sealed),
		Path:     "/",
		MaxAge:   int(saveFeedbackTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func readSaveFeedback(r *http.Request) saveFeedbackData {
	cookie, err := r.Cookie(saveFeedbackCookie)
	if err != nil || cookie.Value == "" {
		return saveFeedbackData{}
	}
	sealed, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return saveFeedbackData{}
	}
	cipher := saveFeedbackCipher()
	if len(sealed) < cipher.NonceSize() {
		return saveFeedbackData{}
	}
	plain, err := cipher.Open(nil, sealed[:cipher.NonceSize()], sealed[cipher.NonceSize():], nil)
	if err != nil {
		return saveFeedbackData{}
	}
	var data saveFeedbackData
	if err := json.Unmarshal(plain, &data); err != nil || data.Message == "" || data.Kind != saveFeedbackSuccess || data.IssuedAt == 0 {
		return saveFeedbackData{}
	}
	issuedAt := time.Unix(data.IssuedAt, 0)
	now := saveFeedbackNow()
	if issuedAt.After(now) || now.Sub(issuedAt) >= saveFeedbackTTL {
		return saveFeedbackData{}
	}
	return data
}

func clearSaveFeedbackCookie(w http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie(saveFeedbackCookie); err == nil {
		http.SetCookie(w, &http.Cookie{Name: saveFeedbackCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	}
}
