package application

import (
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestInstanceLanguageRejectsAdminWithoutWriting(t *testing.T) {
	store := &fakeSettingsStore{language: "en"}
	svc := NewSettingsService(store)
	var forbidden *domain.ForbiddenError
	if err := svc.SetInterfaceLanguage(t.Context(), adminActor(), "es"); !errors.As(err, &forbidden) {
		t.Errorf("admin language write = %v, want forbidden", err)
	}
	if store.language != "en" || store.setLanguageCalls != 0 {
		t.Fatalf("denied write changed language=%q calls=%d", store.language, store.setLanguageCalls)
	}
}
