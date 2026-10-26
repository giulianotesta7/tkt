package application_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

// Local category-suggestion engine (issue #416, S4/S8). These tests own the
// thresholds: the suggester decides whether ranked candidates are confident enough
// to show, and nothing here touches a database. The fake index records what it was
// asked and returns canned candidates, so each case below is a statement about the
// application layer alone.

func TestLocalSuggesterReturnsTheConfidentCandidate(t *testing.T) {
	index := newFakeCategorySuggestionIndex(application.CategorySuggestion{
		CategoryID: 4, Score: 6, MatchedWords: []string{"impresora", "toner"},
	})
	suggester := application.NewLocalCategorySuggester(index)

	got, err := suggester.Suggest(context.Background(), "la impresora no imprime", []int64{4, 5})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 1 || got[0].CategoryID != 4 {
		t.Fatalf("Suggest = %+v, want the one confident candidate", got)
	}
	if fmt.Sprint(got[0].MatchedWords) != fmt.Sprint([]string{"impresora", "toner"}) {
		t.Errorf("matched words = %v, want the words the index reported", got[0].MatchedWords)
	}
	if len(index.texts) != 1 || index.texts[0] != "la impresora no imprime" {
		t.Errorf("the index received %v, want the raw requester text", index.texts)
	}
	if fmt.Sprint(index.allowed[0]) != fmt.Sprint([]int64{4, 5}) {
		t.Errorf("the index received allowed %v, want the caller's set", index.allowed[0])
	}
}

func TestLocalSuggesterRefusesAScoreBelowTheLocalThreshold(t *testing.T) {
	// One distinctive word shared by a quarter of the catalog scores around 1, and
	// the local engine's floor is above it: a weak signal is not a suggestion.
	index := newFakeCategorySuggestionIndex(application.CategorySuggestion{CategoryID: 2, Score: application.LocalSuggestionMinScore - 0.01})
	suggester := application.NewLocalCategorySuggester(index)

	got, err := suggester.Suggest(context.Background(), "office", []int64{1, 2, 3})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Suggest = %+v, want nothing below the local threshold", got)
	}
}

func TestLocalSuggesterRefusesAWordEveryCategoryShares(t *testing.T) {
	// A term present in every document has no discriminating power, so FTS5 ranks
	// it at zero and the negated rank is zero too. Nothing may be suggested.
	index := newFakeCategorySuggestionIndex(
		application.CategorySuggestion{CategoryID: 1, Score: 0},
		application.CategorySuggestion{CategoryID: 2, Score: 0},
	)
	suggester := application.NewLocalCategorySuggester(index)

	got, err := suggester.Suggest(context.Background(), "soporte", []int64{1, 2})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Suggest = %+v, want nothing for a non-discriminating word", got)
	}
}

func TestLocalSuggesterNeverReturnsACategoryOutsideTheAllowedSet(t *testing.T) {
	// The fake returns a disallowed id on purpose: the suggester filters, so the
	// guarantee does not depend on the storage adapter.
	index := newFakeCategorySuggestionIndex(
		application.CategorySuggestion{CategoryID: 99, Score: 9},
		application.CategorySuggestion{CategoryID: 4, Score: 6},
	)
	suggester := application.NewLocalCategorySuggester(index)

	got, err := suggester.Suggest(context.Background(), "impresora", []int64{4, 5})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 1 || got[0].CategoryID != 4 {
		t.Fatalf("Suggest = %+v, want only the allowed category", got)
	}
}

func TestLocalSuggesterCapsAtThreeAndKeepsTheRelativeCutoff(t *testing.T) {
	cases := []struct {
		name       string
		candidates []application.CategorySuggestion
		want       []int64
	}{
		{
			name: "three kept, the fourth is clearly behind",
			candidates: []application.CategorySuggestion{
				{CategoryID: 1, Score: 10},
				{CategoryID: 2, Score: 9},
				{CategoryID: 3, Score: 8},
				{CategoryID: 4, Score: 1},
			},
			want: []int64{1, 2, 3},
		},
		{
			name: "a distant third place is not offered",
			candidates: []application.CategorySuggestion{
				{CategoryID: 1, Score: 10},
				{CategoryID: 2, Score: 3},
			},
			want: []int64{1},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			index := newFakeCategorySuggestionIndex(testCase.candidates...)
			suggester := application.NewLocalCategorySuggester(index)

			got, err := suggester.Suggest(context.Background(), "laptop", []int64{1, 2, 3, 4})
			if err != nil {
				t.Fatalf("Suggest: %v", err)
			}
			ids := make([]int64, len(got))
			for index, suggestion := range got {
				ids[index] = suggestion.CategoryID
			}
			if fmt.Sprint(ids) != fmt.Sprint(testCase.want) {
				t.Fatalf("Suggest ids = %v, want %v (limit %d)", ids, testCase.want, application.CategorySuggestionLimit)
			}
		})
	}
}

func TestLocalSuggesterOffersTwoEquallyGoodCategories(t *testing.T) {
	// Two categories the text describes equally well, and nothing else: there is no
	// "rest" to separate from, so both are offered and the requester chooses — the
	// margin guards against a weak tail, not against a genuine second candidate.
	index := newFakeCategorySuggestionIndex(
		application.CategorySuggestion{CategoryID: 1, Score: 6},
		application.CategorySuggestion{CategoryID: 2, Score: 6},
	)
	suggester := application.NewLocalCategorySuggester(index)

	got, err := suggester.Suggest(context.Background(), "no funciona la red", []int64{1, 2})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Suggest = %+v, want both equally good categories", got)
	}
}

func TestLocalSuggesterRefusesWhenTheBestDoesNotSeparateFromTheRest(t *testing.T) {
	// Four categories the text describes equally well: three would be kept and the
	// fourth left out at the same score, so the best does not separate from the rest
	// and nothing is suggested (S3).
	index := newFakeCategorySuggestionIndex(
		application.CategorySuggestion{CategoryID: 1, Score: 6},
		application.CategorySuggestion{CategoryID: 2, Score: 6},
		application.CategorySuggestion{CategoryID: 3, Score: 6},
		application.CategorySuggestion{CategoryID: 4, Score: 6},
	)
	suggester := application.NewLocalCategorySuggester(index)

	got, err := suggester.Suggest(context.Background(), "no funciona la red", []int64{1, 2, 3, 4})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Suggest = %+v, want nothing when the top does not separate", got)
	}
}

func TestLocalSuggesterRefusesWithoutUsableInput(t *testing.T) {
	index := newFakeCategorySuggestionIndex(application.CategorySuggestion{CategoryID: 1, Score: 9})
	suggester := application.NewLocalCategorySuggester(index)

	for _, testCase := range []struct {
		name    string
		text    string
		allowed []int64
	}{
		{name: "blank text", text: "   ", allowed: []int64{1}},
		{name: "no allowed category", text: "impresora", allowed: nil},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := suggester.Suggest(context.Background(), testCase.text, testCase.allowed)
			if err != nil {
				t.Fatalf("Suggest: %v", err)
			}
			if len(got) != 0 {
				t.Fatalf("Suggest = %+v, want nothing", got)
			}
		})
	}
	if len(index.texts) != 0 {
		t.Errorf("the index was queried %v, want no query when there is nothing to ask", index.texts)
	}
}

func TestLocalSuggesterPropagatesIndexFailure(t *testing.T) {
	index := newFakeCategorySuggestionIndex()
	index.err = errors.New("index unavailable")
	suggester := application.NewLocalCategorySuggester(index)

	if _, err := suggester.Suggest(context.Background(), "impresora", []int64{1}); err == nil {
		t.Fatal("Suggest succeeded, want the index failure")
	}
}

func TestSuggestionTermsNormalizesTheRequesterText(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "capitals and accents fold",
			text: "CONEXIÓN Wi-Fi",
			want: []string{"conexion", "wi", "fi"},
		},
		{
			name: "spanish stopwords and short tokens drop",
			text: "La impresora del tercer piso no imprime",
			want: []string{"impresora", "tercer", "piso", "imprime"},
		},
		{
			name: "english stopwords drop",
			text: "the printer is not working",
			want: []string{"printer", "working"},
		},
		{
			name: "punctuation separates words and duplicates collapse",
			text: "toner, toner; TÓNER. papel!",
			want: []string{"toner", "papel"},
		},
		{
			name: "digits are words",
			text: "error 500 en el servidor",
			want: []string{"error", "500", "servidor"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := application.SuggestionTerms(testCase.text)
			if strings.Join(got, "|") != strings.Join(testCase.want, "|") {
				t.Errorf("SuggestionTerms(%q) = %v, want %v", testCase.text, got, testCase.want)
			}
		})
	}
}
