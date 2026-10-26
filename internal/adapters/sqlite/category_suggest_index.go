package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
)

// categorySuggestionIndex ranks categories for the local suggestion engine
// (issue #416, S4) over the FTS5 indexes migration 0031 creates.
//
// The split with the application layer is deliberate and strict: this adapter
// answers "what do the indexes rank highest for this text, and which words of the
// text did each candidate contain". It owns normalization, typo correction,
// ranking and matched-word attribution, because all four are properties of the
// index. It owns no threshold — S8 keeps those per engine, in the application
// layer — and it never decides that a match is good enough.
type categorySuggestionIndex struct {
	db *sql.DB
}

var _ application.CategorySuggestionIndex = (*categorySuggestionIndex)(nil)

func newCategorySuggestionIndex(db *sql.DB) *categorySuggestionIndex {
	return &categorySuggestionIndex{db: db}
}

// The BM25 column weights, in the column order of category_suggest_fts: name,
// keywords, description, desk, department.
//
// Name and keywords carry what an administrator decided this category is about,
// and a keyword is literally a word a requester might type, so both sit far above
// the description, which is prose about the category and matches incidentally. The
// desk and department names are the weakest signal on purpose: every category of a
// desk shares them, so they help place a category but never distinguish one from
// its siblings. The gaps are ordinal, not calibrated: bm25 is scale-free per term,
// so only the ratio between columns is meaningful.
const (
	suggestionNameWeight        = 10.0
	suggestionKeywordsWeight    = 8.0
	suggestionDescriptionWeight = 1.0
	suggestionPlaceWeight       = 0.5
)

// suggestionTrigramFloor is the lowest trigram Jaccard similarity a candidate word
// must reach before a query word is replaced by it. A single edit in a word of six
// or more runes lands around 0.4, while unrelated words share no trigram at all,
// so the floor sits just under the first group. Below it, a "correction" would be
// a guess at a different word.
const suggestionTrigramFloor = 0.35

// suggestionTrigramRowLimit bounds how many trigram rows one correction considers:
// enough to find the intended word in a catalog of this size, small enough that a
// broad fragment does not turn into a full scan.
const suggestionTrigramRowLimit = 50

// SuggestCandidates ranks the allowed categories for text.
//
// Pipeline, in order: normalize with application.SuggestionTerms (the same
// normalization the indexes were built with), replace query words the vocabulary
// does not know with the indexed word they most resemble, rank every allowed
// category that matches any of those words by weighted BM25, and report for each
// the words it matched. A text whose words are all unknown, or all stopwords,
// produces no candidates — not an error.
func (i *categorySuggestionIndex) SuggestCandidates(ctx context.Context, text string, allowed []int64) ([]application.CategorySuggestion, error) {
	if len(allowed) == 0 {
		return nil, nil
	}
	words := application.SuggestionTerms(text)
	if len(words) == 0 {
		return nil, nil
	}
	words, err := i.resolveTypos(ctx, words)
	if err != nil {
		return nil, err
	}
	if len(words) == 0 {
		return nil, nil
	}

	ids, args := idList(allowed)
	query := fmt.Sprintf(`SELECT rowid, bm25(category_suggest_fts, %s)
		FROM category_suggest_fts
		WHERE category_suggest_fts MATCH ? AND rowid IN (%s)
		ORDER BY bm25(category_suggest_fts, %s) ASC, rowid ASC
		LIMIT ?`,
		suggestionWeights(),
		ids,
		suggestionWeights())
	args = append([]any{suggestionMatch(words)}, args...)
	args = append(args, application.CategorySuggestionCandidateLimit)

	rows, err := i.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: rank category suggestions: %w", err)
	}
	defer rows.Close()

	var out []application.CategorySuggestion
	for rows.Next() {
		var (
			id   int64
			rank float64
		)
		if err := rows.Scan(&id, &rank); err != nil {
			return nil, fmt.Errorf("sqlite: scan category suggestion: %w", err)
		}
		out = append(out, application.CategorySuggestion{CategoryID: id, Score: -rank})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: rank category suggestions: %w", err)
	}
	if len(out) == 0 {
		return nil, nil
	}

	idToWords, err := i.matchedWords(ctx, words, out)
	if err != nil {
		return nil, err
	}
	for index := range out {
		out[index].MatchedWords = idToWords[out[index].CategoryID]
	}
	return out, nil
}

// suggestionWeights renders the column weights as the literal list bm25 takes.
// They are constants, and writing them into the statement keeps the call readable
// next to the column order it depends on.
func suggestionWeights() string {
	return strconv.FormatFloat(suggestionNameWeight, 'f', -1, 64) + ", " +
		strconv.FormatFloat(suggestionKeywordsWeight, 'f', -1, 64) + ", " +
		strconv.FormatFloat(suggestionDescriptionWeight, 'f', -1, 64) + ", " +
		strconv.FormatFloat(suggestionPlaceWeight, 'f', -1, 64) + ", " +
		strconv.FormatFloat(suggestionPlaceWeight, 'f', -1, 64)
}

// suggestionMatch builds the FTS5 expression: every word quoted and joined with
// OR, so a description reaches any category one of its words names and the ranks
// decide the rest. SuggestionTerms only emits letters and digits, so a quoted word
// can never carry FTS5 syntax.
func suggestionMatch(words []string) string {
	quoted := make([]string, len(words))
	for index, word := range words {
		quoted[index] = `"` + word + `"`
	}
	return strings.Join(quoted, " OR ")
}

// idList renders "?,?,?" and its arguments for an IN clause.
func idList(ids []int64) (string, []any) {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for index, id := range ids {
		placeholders[index] = "?"
		args[index] = id
	}
	return strings.Join(placeholders, ","), args
}

// matchedWords reports, for each candidate, which of the query words its indexed
// text contains, in the order the requester wrote them. The text is read from the
// catalog tables rather than from the index, because the index stores the same
// fields the engine ranks over and this is the answer a picker labels a suggestion
// with.
func (i *categorySuggestionIndex) matchedWords(ctx context.Context, words []string, candidates []application.CategorySuggestion) (map[int64][]string, error) {
	ids := make([]int64, len(candidates))
	for index, candidate := range candidates {
		ids[index] = candidate.CategoryID
	}
	in, args := idList(ids)
	rows, err := i.db.QueryContext(ctx, `SELECT c.id, c.name, c.keywords, c.description,
			COALESCE(ds.name, ''), COALESCE(d.name, '')
		FROM categories c
		LEFT JOIN desks ds ON ds.id = c.desk_id
		LEFT JOIN departments d ON d.id = ds.department_id
		WHERE c.id IN (`+in+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: read suggestion matches: %w", err)
	}
	defer rows.Close()

	out := make(map[int64][]string, len(ids))
	for rows.Next() {
		var (
			id                                            int64
			name, keywords, description, desk, department string
		)
		if err := rows.Scan(&id, &name, &keywords, &description, &desk, &department); err != nil {
			return nil, fmt.Errorf("sqlite: scan suggestion matches: %w", err)
		}
		indexed := make(map[string]struct{})
		for _, word := range application.SuggestionTerms(strings.Join([]string{name, keywords, description, desk, department}, " ")) {
			indexed[word] = struct{}{}
		}
		var matched []string
		for _, word := range words {
			if _, ok := indexed[word]; ok {
				matched = append(matched, word)
			}
		}
		out[id] = matched
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: read suggestion matches: %w", err)
	}
	return out, nil
}

// resolveTypos keeps the query words the index already knows and replaces the ones
// it does not with the indexed word they most resemble. A word with no plausible
// correction is dropped: searching for it would only match categories by accident.
func (i *categorySuggestionIndex) resolveTypos(ctx context.Context, words []string) ([]string, error) {
	known, err := i.vocabulary(ctx, words)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(words))
	seen := make(map[string]struct{}, len(words))
	for _, word := range words {
		resolved := word
		if !known[word] {
			corrected, found, err := i.correctTypo(ctx, word)
			if err != nil {
				return nil, err
			}
			if !found {
				continue
			}
			resolved = corrected
		}
		if _, dup := seen[resolved]; dup {
			continue
		}
		seen[resolved] = struct{}{}
		out = append(out, resolved)
	}
	return out, nil
}

// vocabulary reports which of the words the main index already holds. fts5vocab is
// the vocabulary itself, so this is a lookup per word instead of a search that
// would have to guess whether an empty result meant "unknown word" or "no match".
func (i *categorySuggestionIndex) vocabulary(ctx context.Context, words []string) (map[string]bool, error) {
	in, args := idListStrings(words)
	rows, err := i.db.QueryContext(ctx,
		`SELECT term FROM category_suggest_vocab WHERE term IN (`+in+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: read suggestion vocabulary: %w", err)
	}
	defer rows.Close()
	out := make(map[string]bool, len(words))
	for rows.Next() {
		var term string
		if err := rows.Scan(&term); err != nil {
			return nil, fmt.Errorf("sqlite: scan suggestion vocabulary: %w", err)
		}
		out[term] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: read suggestion vocabulary: %w", err)
	}
	return out, nil
}

// correctTypo finds the indexed word most similar to word, or reports that none of
// them is similar enough.
//
// The trigram index narrows the search first: the OR of the word's trigrams returns
// the rows of category_suggest_trigram that share at least one of them, and only the
// words of those rows are candidates. Similarity is then the Jaccard index of the
// two trigram sets, computed here because SQLite has no such function. Candidates
// are visited in sorted order so an exact tie resolves the same way on every run.
func (i *categorySuggestionIndex) correctTypo(ctx context.Context, word string) (string, bool, error) {
	trigrams := suggestionTrigrams(word)
	if len(trigrams) == 0 {
		return "", false, nil
	}
	rows, err := i.db.QueryContext(ctx,
		`SELECT rowid FROM category_suggest_trigram WHERE category_suggest_trigram MATCH ? LIMIT ?`,
		suggestionTrigramMatch(trigrams), suggestionTrigramRowLimit)
	if err != nil {
		return "", false, fmt.Errorf("sqlite: trigram lookup for %q: %w", word, err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return "", false, fmt.Errorf("sqlite: scan trigram match: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", false, fmt.Errorf("sqlite: trigram lookup for %q: %w", word, err)
	}
	if len(ids) == 0 {
		return "", false, nil
	}

	in, args := idList(ids)
	candidates, err := i.trigramCandidateWords(ctx, in, args)
	if err != nil {
		return "", false, err
	}
	best := ""
	bestScore := suggestionTrigramFloor
	for _, candidate := range candidates {
		score := trigramJaccard(trigrams, suggestionTrigrams(candidate))
		if score > bestScore {
			best, bestScore = candidate, score
		}
	}
	if best == "" {
		return "", false, nil
	}
	return best, true, nil
}

// trigramCandidateWords returns the words of the matched rows' name and keywords,
// sorted and deduplicated. Those two fields are the only ones the trigram index
// holds, so a typo of a description word has no candidates here by design: the
// description is prose, and "correcting" one of its words would match a category
// for a word it never used.
func (i *categorySuggestionIndex) trigramCandidateWords(ctx context.Context, in string, args []any) ([]string, error) {
	rows, err := i.db.QueryContext(ctx,
		`SELECT name, keywords FROM categories WHERE id IN (`+in+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: read trigram candidates: %w", err)
	}
	defer rows.Close()
	seen := make(map[string]struct{})
	var out []string
	for rows.Next() {
		var name, keywords string
		if err := rows.Scan(&name, &keywords); err != nil {
			return nil, fmt.Errorf("sqlite: scan trigram candidate: %w", err)
		}
		for _, word := range application.SuggestionTerms(name + " " + keywords) {
			if len(suggestionTrigrams(word)) == 0 {
				continue
			}
			if _, dup := seen[word]; dup {
				continue
			}
			seen[word] = struct{}{}
			out = append(out, word)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: read trigram candidates: %w", err)
	}
	sort.Strings(out)
	return out, nil
}

// idListStrings is idList for TEXT bind parameters.
func idListStrings(values []string) (string, []any) {
	placeholders := make([]string, len(values))
	args := make([]any, len(values))
	for index, value := range values {
		placeholders[index] = "?"
		args[index] = value
	}
	return strings.Join(placeholders, ","), args
}

// suggestionTrigrams returns the set of three-rune windows of word. A word shorter
// than three runes has none, which is also the shortest query the trigram tokenizer
// can match.
func suggestionTrigrams(word string) map[string]struct{} {
	runes := []rune(word)
	if len(runes) < 3 {
		return nil
	}
	out := make(map[string]struct{}, len(runes)-2)
	for start := 0; start+3 <= len(runes); start++ {
		out[string(runes[start:start+3])] = struct{}{}
	}
	return out
}

// suggestionTrigramMatch builds trigram OR expression, quoted so a trigram that
// happens to look like FTS5 syntax cannot change the query's meaning.
func suggestionTrigramMatch(trigrams map[string]struct{}) string {
	terms := make([]string, 0, len(trigrams))
	for trigram := range trigrams {
		terms = append(terms, `"`+trigram+`"`)
	}
	sort.Strings(terms)
	return strings.Join(terms, " OR ")
}

// trigramJaccard is the size of the intersection over the size of the union of two
// trigram sets: 1 for identical words, and 0 when they share nothing.
func trigramJaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	shared := 0
	for trigram := range a {
		if _, ok := b[trigram]; ok {
			shared++
		}
	}
	union := len(a) + len(b) - shared
	if union == 0 {
		return 0
	}
	return float64(shared) / float64(union)
}
