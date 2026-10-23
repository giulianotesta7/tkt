package application

import (
	"context"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Category suggestions, local engine (issue #416, S4). Two pieces live here and
// nothing else does: the ranked-candidate PORT the storage adapter implements,
// and the application-layer suggester that turns ranked candidates into the at
// most three suggestions the pickers show. Everything the local engine decides
// about confidence is therefore testable without a database, and the storage
// adapter owns only "what does the index rank highest for this text".
//
// The decision-model engine (S5) is a different port with its own thresholds:
// S8 says a number tuned for one engine is never reused for the other, which is
// why every constant below is named for the local engine.

// CategorySuggestionLimit is how many categories the local engine may suggest.
// Three is the product's ceiling (S4, and the pickers' layout), not a ranking
// choice.
const CategorySuggestionLimit = 3

// CategorySuggestionCandidateLimit is how many ranked candidates the storage
// adapter returns for the suggester to work with. It is deliberately larger than
// CategorySuggestionLimit: the suggester compares the suggestions it keeps
// against the first one it drops, so it needs to see at least one more than it
// could ever return, and a handful more keeps that comparison meaningful when
// several categories tie near the cutoff.
const CategorySuggestionCandidateLimit = 10

// CategorySuggestion is one ranked candidate for the local engine.
type CategorySuggestion struct {
	// CategoryID identifies the category. The suggester only ever returns ids in
	// the allowed set it was given.
	CategoryID int64
	// Score is the relevance of the category for the text: the negated SQLite
	// FTS5 BM25 rank, so a larger score is a better match. A word that appears in
	// every category contributes nothing, and a text that matches no indexed word
	// produces no candidate at all.
	Score float64
	// MatchedWords are the normalized query words this category's indexed text
	// contains, in the order they were written. A typo is reported as the indexed
	// word it was corrected to, because that is the word the requester's text
	// actually reached.
	MatchedWords []string
}

// CategorySuggestionIndex is the read port behind the local engine: one query
// text in, the best-ranked categories out, restricted to the categories the
// caller allows.
//
// The adapter owns normalization, typo correction, ranking and matched-word
// attribution, because all four are properties of the index. The application
// layer owns the thresholds, because those are product decisions.
type CategorySuggestionIndex interface {
	// SuggestCandidates ranks the allowed categories for text and returns at
	// most CategorySuggestionCandidateLimit of them, best first. It returns no
	// candidates when the text carries no usable word or nothing matches, and it
	// never returns a category outside allowed.
	SuggestCandidates(ctx context.Context, text string, allowed []int64) ([]CategorySuggestion, error)
}

// LocalCategorySuggester applies the local engine's thresholds to the ranked
// candidates.
type LocalCategorySuggester struct {
	index CategorySuggestionIndex
}

// NewLocalCategorySuggester wires the local engine over its index.
func NewLocalCategorySuggester(index CategorySuggestionIndex) *LocalCategorySuggester {
	return &LocalCategorySuggester{index: index}
}

// The local engine's thresholds (S4, S8). Each one exists to keep the feature
// from guessing: a suggestion is offered only when the requester's text names
// this category clearly better than anything else in the catalog.
const (
	// LocalSuggestionMinScore is the score the best candidate must reach on its
	// own. The scale is BM25-derived, so the number is empirical: one distinctive
	// word of a category name, keyword or description reaches it comfortably,
	// while a word shared by a quarter of the catalog stays below it. A score of
	// zero (a term present in every category contributes nothing) never clears it.
	LocalSuggestionMinScore = 1.5
	// LocalSuggestionRelativeCutoff keeps only candidates that score at least
	// this fraction of the best one, so a distant third place is not offered as an
	// equally plausible choice.
	LocalSuggestionRelativeCutoff = 0.5
	// LocalSuggestionMargin is how far the best candidate must beat the first one
	// that was left out, as a fraction of its own score. Two categories the text
	// describes equally well produce no suggestion at all: the requester would be
	// picking between two guesses, which is worse than the catalog as it is today.
	LocalSuggestionMargin = 0.2
)

// Suggest returns the categories the local engine is confident about for text,
// best first, at most CategorySuggestionLimit of them, and only from allowed.
//
// It returns nothing — no error, no partial list — when the text names no
// category clearly: S3 says the requester still chooses, and with no confident
// suggestion the catalog behaves exactly as it does today.
func (s *LocalCategorySuggester) Suggest(ctx context.Context, text string, allowed []int64) ([]CategorySuggestion, error) {
	if s == nil || s.index == nil || len(allowed) == 0 || strings.TrimSpace(text) == "" {
		return nil, nil
	}
	candidates, err := s.index.SuggestCandidates(ctx, text, allowed)
	if err != nil {
		return nil, err
	}

	allowedSet := make(map[int64]struct{}, len(allowed))
	for _, id := range allowed {
		allowedSet[id] = struct{}{}
	}

	// The port already excludes disallowed ids; filtering again is what makes
	// "never returns others" a property of this type rather than a promise about
	// a collaborator, and it costs one map lookup per candidate.
	ranked := make([]CategorySuggestion, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := allowedSet[candidate.CategoryID]; !ok {
			continue
		}
		if candidate.Score <= 0 {
			continue
		}
		ranked = append(ranked, candidate)
	}
	if len(ranked) == 0 {
		return nil, nil
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })

	top := ranked[0].Score
	if top < LocalSuggestionMinScore {
		return nil, nil
	}

	keep := 0
	for keep < len(ranked) && keep < CategorySuggestionLimit && ranked[keep].Score >= top*LocalSuggestionRelativeCutoff {
		keep++
	}
	if keep == 0 {
		return nil, nil
	}
	// The first candidate that was not kept is what "the rest" means here: either
	// it fell below the relative cutoff, or it was the fourth-placed one. When
	// every candidate is kept there is nothing to separate from, so the margin
	// holds by definition.
	if keep < len(ranked) && top-ranked[keep].Score < top*LocalSuggestionMargin {
		return nil, nil
	}
	return ranked[:keep], nil
}

// The normalization the local engine applies to requester text before it reaches
// the index. It mirrors the tokenizer the indexes were built with, so a word is
// compared with the same shape it was stored in.

// suggestionMinTokenRunes drops the shortest tokens. One-letter words carry no
// signal, and after stopwords are gone what remains of them is noise ("a", "y").
const suggestionMinTokenRunes = 2

// SuggestionTerms normalizes text into the words the local engine searches with:
// lowercase, accents folded, split on anything that is not a letter or a digit,
// Spanish and English stopwords removed, tokens shorter than two runes removed,
// and duplicates collapsed in first-seen order.
//
// It is exported because both sides of the port need the same answer: the storage
// adapter builds its FTS5 query from these words, and any caller that wants to
// show what the engine looked for must not invent a second normalization. Folding
// covers the Latin letters the shipped catalogues can hold (Spanish included);
// marks outside that range are left alone rather than guessed at.
func SuggestionTerms(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		word := foldSuggestionWord(field)
		if utf8.RuneCountInString(word) < suggestionMinTokenRunes {
			continue
		}
		if _, stop := suggestionStopwords[word]; stop {
			continue
		}
		if _, dup := seen[word]; dup {
			continue
		}
		seen[word] = struct{}{}
		out = append(out, word)
	}
	return out
}

// foldSuggestionWord lowercases and unaccentuates one token.
func foldSuggestionWord(word string) string {
	var b strings.Builder
	b.Grow(len(word))
	for _, r := range word {
		b.WriteRune(foldSuggestionRune(r))
	}
	return b.String()
}

// foldSuggestionRune lowercases one rune and replaces a Latin letter that carries
// a diacritic with its base letter, which is what `remove_diacritics` does on the
// index side.
func foldSuggestionRune(r rune) rune {
	r = unicode.ToLower(r)
	if folded, ok := suggestionLatinFolds[r]; ok {
		return folded
	}
	return r
}

// suggestionLatinFolds maps the accented Latin letters of Latin-1 Supplement and
// Latin Extended-A to their base letters.
var suggestionLatinFolds = map[rune]rune{
	'à': 'a', 'á': 'a', 'â': 'a', 'ã': 'a', 'ä': 'a', 'å': 'a', 'ā': 'a', 'ă': 'a', 'ą': 'a',
	'ç': 'c', 'ć': 'c', 'ĉ': 'c', 'ċ': 'c', 'č': 'c',
	'ď': 'd', 'đ': 'd',
	'è': 'e', 'é': 'e', 'ê': 'e', 'ë': 'e', 'ē': 'e', 'ĕ': 'e', 'ė': 'e', 'ę': 'e', 'ě': 'e',
	'ĝ': 'g', 'ğ': 'g', 'ġ': 'g', 'ģ': 'g',
	'ĥ': 'h', 'ħ': 'h',
	'ì': 'i', 'í': 'i', 'î': 'i', 'ï': 'i', 'ĩ': 'i', 'ī': 'i', 'ĭ': 'i', 'į': 'i', 'ı': 'i',
	'ĵ': 'j', 'ķ': 'k',
	'ĺ': 'l', 'ļ': 'l', 'ľ': 'l', 'ŀ': 'l', 'ł': 'l',
	'ñ': 'n', 'ń': 'n', 'ņ': 'n', 'ň': 'n',
	'ò': 'o', 'ó': 'o', 'ô': 'o', 'õ': 'o', 'ö': 'o', 'ø': 'o', 'ō': 'o', 'ŏ': 'o', 'ő': 'o',
	'ŕ': 'r', 'ŗ': 'r', 'ř': 'r',
	'ś': 's', 'ŝ': 's', 'ş': 's', 'š': 's', 'ș': 's',
	'ţ': 't', 'ť': 't', 'ŧ': 't', 'ț': 't',
	'ù': 'u', 'ú': 'u', 'û': 'u', 'ü': 'u', 'ũ': 'u', 'ū': 'u', 'ŭ': 'u', 'ů': 'u', 'ű': 'u', 'ų': 'u',
	'ŵ': 'w', 'ý': 'y', 'ÿ': 'y',
	'ź': 'z', 'ż': 'z', 'ž': 'z',
}

// suggestionStopwords are the function words the local engine drops before it
// ranks, in Spanish and English, written in their folded form. They are the words
// a description uses to hold its content words together, so keeping them only
// adds noise to the query; "no" and "not" are here because a negated sentence
// still names its category through the words around them, and indexing the
// negation itself matched far too much.
var suggestionStopwords = func() map[string]struct{} {
	words := []string{
		// Spanish
		"de", "del", "la", "el", "los", "las", "un", "una", "unos", "unas", "y", "o", "u",
		"que", "en", "con", "por", "para", "al", "se", "su", "sus", "mi", "mis", "tu", "tus",
		"es", "son", "esta", "estan", "estoy", "estas", "estos", "este", "eso", "esa", "esos", "esas",
		"no", "me", "te", "nos", "les", "le", "lo", "como", "mas", "pero", "si", "ya", "hay",
		"muy", "sin", "sobre", "desde", "hasta", "hacia", "entre", "cuando", "donde", "cual", "cuales",
		"quien", "quienes", "porque", "tambien", "todo", "toda", "todos", "todas", "algo", "nada",
		"mucho", "muchos", "poco", "pocos", "hace", "hacer", "hice", "tengo", "tiene", "tienen",
		"ser", "estar", "era", "fue", "han", "ha", "he", "soy", "eres", "somos", "ante", "bajo",
		"contra", "durante", "mediante", "segun", "ni", "tan", "tanto", "otro", "otra", "otros", "otras",
		// English
		"the", "and", "or", "of", "to", "in", "on", "at", "for", "with", "from", "by", "is", "are",
		"was", "were", "be", "been", "being", "have", "has", "had", "do", "does", "did", "not",
		"my", "we", "you", "he", "she", "it", "they", "this", "that", "these", "those", "there",
		"here", "when", "what", "which", "who", "how", "can", "cannot", "cant", "could", "should",
		"would", "will", "just", "about", "into", "over", "under", "after", "before", "but", "if",
		"so", "than", "then", "too", "very", "also", "im", "ive", "dont", "didnt", "doesnt",
	}
	out := make(map[string]struct{}, len(words))
	for _, word := range words {
		out[word] = struct{}{}
	}
	return out
}()
