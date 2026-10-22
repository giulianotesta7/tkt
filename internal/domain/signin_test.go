package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Sign-in panel rules (issue #383, S9/S10): every text field is plain text with
// a documented limit, the three closed sets (middle content, notice kind, icon)
// are enforced here, and the background image must prove its type by magic
// bytes.

// signinPNG and signinJPEG are images whose magic bytes prove their type.
var (
	signinPNG  = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	signinJPEG = append([]byte("\xFF\xD8\xFF\xE0"), make([]byte, 64)...)
)

func validSigninInput() domain.SigninInput {
	return domain.SigninInput{Panel: domain.SigninPanel{
		Title:    "Sistemas, a un pedido de distancia",
		Subtitle: "Cargá tu pedido y seguí su estado.",
		Content:  domain.SigninContentSteps,
	}}
}

func TestNormalizeSigninTrimsAndDefaults(t *testing.T) {
	in := validSigninInput()
	in.Panel.Title = "  Welcome  "
	in.Panel.Subtitle = "  Everything you need  "

	got, err := domain.NormalizeSignin(in, false)
	if err != nil {
		t.Fatalf("NormalizeSignin: %v", err)
	}
	if got.Title != "Welcome" || got.Subtitle != "Everything you need" {
		t.Errorf("panel = %+v, want the trimmed values", got)
	}

	// The empty submission is the documented default: steps, no notice, the
	// dark background, and no contact line beyond what Organization stores.
	got, err = domain.NormalizeSignin(domain.SigninInput{}, false)
	if err != nil {
		t.Fatalf("NormalizeSignin(empty): %v", err)
	}
	if got.Content != domain.SigninContentSteps || got.Background != domain.SigninBackgroundDark {
		t.Errorf("defaults = %+v, want steps on a dark background", got)
	}
	if got.NoticeEnabled || len(got.Highlights) != 0 {
		t.Errorf("defaults = %+v, want no notice and no highlights", got)
	}
}

func TestNormalizeSigninRejectsEveryBrokenField(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	cases := []struct {
		name  string
		panel domain.SigninPanel
		field string
		key   string
	}{
		{"title too long", domain.SigninPanel{Title: long(domain.MaxSigninTitleRunes + 1)}, "title", "error.signin_title_invalid"},
		{"title with a line break", domain.SigninPanel{Title: "Two\nlines"}, "title", "error.signin_title_invalid"},
		{"subtitle too long", domain.SigninPanel{Subtitle: long(domain.MaxSigninSubtitleRunes + 1)}, "subtitle", "error.signin_subtitle_invalid"},
		{"unknown content", domain.SigninPanel{Content: "gallery"}, "content", "error.signin_content_invalid"},
		{"too many highlights", domain.SigninPanel{Content: domain.SigninContentHighlights, Highlights: []domain.SigninHighlight{
			{Icon: "clock", Title: "a"}, {Icon: "clock", Title: "b"}, {Icon: "clock", Title: "c"}, {Icon: "clock", Title: "d"},
		}}, "highlights", "error.signin_highlights_too_many"},
		{"unknown icon", domain.SigninPanel{Content: domain.SigninContentHighlights, Highlights: []domain.SigninHighlight{
			{Icon: "rocket", Title: "Fast"},
		}}, "highlights", "error.signin_highlight_invalid"},
		{"highlight without a title", domain.SigninPanel{Content: domain.SigninContentHighlights, Highlights: []domain.SigninHighlight{
			{Icon: "clock", Text: "only text"},
		}}, "highlights", "error.signin_highlight_invalid"},
		{"highlight title too long", domain.SigninPanel{Content: domain.SigninContentHighlights, Highlights: []domain.SigninHighlight{
			{Icon: "clock", Title: long(domain.MaxSigninHighlightTitleRunes + 1)},
		}}, "highlights", "error.signin_highlight_invalid"},
		{"highlight text too long", domain.SigninPanel{Content: domain.SigninContentHighlights, Highlights: []domain.SigninHighlight{
			{Icon: "clock", Title: "Fast", Text: long(domain.MaxSigninHighlightTextRunes + 1)},
		}}, "highlights", "error.signin_highlight_invalid"},
		{"notice without a title", domain.SigninPanel{NoticeEnabled: true, NoticeKind: domain.SigninNoticeInfo}, "notice", "error.signin_notice_title_required"},
		{"notice title too long", domain.SigninPanel{NoticeEnabled: true, NoticeTitle: long(domain.MaxSigninNoticeTitleRunes + 1)}, "notice", "error.signin_notice_invalid"},
		{"notice text too long", domain.SigninPanel{NoticeEnabled: true, NoticeTitle: "Maintenance", NoticeText: long(domain.MaxSigninNoticeTextRunes + 1)}, "notice", "error.signin_notice_invalid"},
		{"unknown notice kind", domain.SigninPanel{NoticeEnabled: true, NoticeKind: "error", NoticeTitle: "Maintenance"}, "notice", "error.signin_notice_invalid"},
		{"unknown background", domain.SigninPanel{Background: "gradient"}, "background", "error.signin_background_invalid"},
		{"brand color missing", domain.SigninPanel{Background: domain.SigninBackgroundBrand}, "brand_color", "error.signin_brand_color_invalid"},
		{"brand color malformed", domain.SigninPanel{Background: domain.SigninBackgroundBrand, BrandColor: "#C81"}, "brand_color", "error.signin_brand_color_invalid"},
		{"phone too long", domain.SigninPanel{SupportPhone: long(domain.MaxSigninSupportPhoneRunes + 1)}, "support_phone", "error.signin_contact_invalid"},
		{"hours too long", domain.SigninPanel{SupportHours: long(domain.MaxSigninSupportHoursRunes + 1)}, "support_hours", "error.signin_contact_invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := domain.NormalizeSignin(domain.SigninInput{Panel: tc.panel}, false)
			var validation *domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("err = %v, want a ValidationError", err)
			}
			if validation.Field != tc.field {
				t.Errorf("field = %q, want %q", validation.Field, tc.field)
			}
			if validation.Key != tc.key {
				t.Errorf("key = %q, want %q", validation.Key, tc.key)
			}
		})
	}
}

// TestNormalizeSigninAcceptsTheBoundaries proves every cap is inclusive and the
// closed sets accept exactly their documented members.
func TestNormalizeSigninAcceptsTheBoundaries(t *testing.T) {
	panel := domain.SigninPanel{
		Title:    strings.Repeat("t", domain.MaxSigninTitleRunes),
		Subtitle: strings.Repeat("s", domain.MaxSigninSubtitleRunes),
		Content:  domain.SigninContentHighlights,
		Highlights: []domain.SigninHighlight{
			{Icon: domain.SigninIconClock, Title: strings.Repeat("h", domain.MaxSigninHighlightTitleRunes), Text: strings.Repeat("x", domain.MaxSigninHighlightTextRunes)},
			{Icon: domain.SigninIconPhone, Title: "Phone"},
			{Icon: domain.SigninIconCheck, Title: "Check"},
		},
		NoticeEnabled: true,
		NoticeKind:    domain.SigninNoticeWarning,
		NoticeTitle:   strings.Repeat("n", domain.MaxSigninNoticeTitleRunes),
		NoticeText:    strings.Repeat("b", domain.MaxSigninNoticeTextRunes),
		Background:    domain.SigninBackgroundBrand,
		BrandColor:    "#C8102E",
		SupportPhone:  strings.Repeat("p", domain.MaxSigninSupportPhoneRunes),
		SupportHours:  strings.Repeat("o", domain.MaxSigninSupportHoursRunes),
	}
	if _, err := domain.NormalizeSignin(domain.SigninInput{Panel: panel}, false); err != nil {
		t.Fatalf("boundary submission rejected: %v", err)
	}

	// Every icon of the closed set is accepted, and nothing else is.
	for _, icon := range domain.SigninIconOptions() {
		in := validSigninInput()
		in.Panel.Content = domain.SigninContentHighlights
		in.Panel.Highlights = []domain.SigninHighlight{{Icon: icon, Title: "Titled"}}
		if _, err := domain.NormalizeSignin(in, false); err != nil {
			t.Errorf("icon %q rejected: %v", icon, err)
		}
	}
	for _, content := range domain.SigninContentOptions() {
		in := validSigninInput()
		in.Panel.Content = content
		if _, err := domain.NormalizeSignin(in, false); err != nil {
			t.Errorf("content %q rejected: %v", content, err)
		}
	}
	for _, background := range []string{domain.SigninBackgroundDark, domain.SigninBackgroundImage} {
		in := validSigninInput()
		in.Panel.Background = background
		if _, err := domain.NormalizeSignin(in, background == domain.SigninBackgroundImage); err != nil {
			t.Errorf("background %q rejected: %v", background, err)
		}
	}
}

// TestNormalizeSigninDropsEmptyHighlightRows proves an untouched "add a
// highlight" row does not block the save: only rows that carry something are
// kept, and only those are validated.
func TestNormalizeSigninDropsEmptyHighlightRows(t *testing.T) {
	in := validSigninInput()
	in.Panel.Content = domain.SigninContentHighlights
	in.Panel.Highlights = []domain.SigninHighlight{
		{},
		// The form always submits an icon, so an untouched slot arrives with one
		// and nothing else.
		{Icon: domain.SigninIconClock},
		{Icon: domain.SigninIconChat, Title: "Chat with us"},
		{Text: ""},
	}
	got, err := domain.NormalizeSignin(in, false)
	if err != nil {
		t.Fatalf("NormalizeSignin: %v", err)
	}
	if len(got.Highlights) != 1 || got.Highlights[0].Title != "Chat with us" {
		t.Errorf("highlights = %+v, want the one filled row", got.Highlights)
	}
}

// TestNormalizeSigninClearsADisabledNotice proves a notice switched off stores
// nothing: the panel the renderer reads cannot carry text nobody sees.
func TestNormalizeSigninClearsADisabledNotice(t *testing.T) {
	in := validSigninInput()
	in.Panel.NoticeEnabled = false
	in.Panel.NoticeKind = domain.SigninNoticeWarning
	in.Panel.NoticeTitle = "Old maintenance"
	in.Panel.NoticeText = "Saturday"

	got, err := domain.NormalizeSignin(in, false)
	if err != nil {
		t.Fatalf("NormalizeSignin: %v", err)
	}
	if got.NoticeKind != "" || got.NoticeTitle != "" || got.NoticeText != "" {
		t.Errorf("disabled notice kept %+v, want it cleared", got)
	}
}

// TestNormalizeSigninRequiresAnImageForTheImageBackground proves the one
// cross-field rule: "image" without bytes would render an empty panel, so it is
// refused unless the instance already stores one.
func TestNormalizeSigninRequiresAnImageForTheImageBackground(t *testing.T) {
	in := validSigninInput()
	in.Panel.Background = domain.SigninBackgroundImage

	_, err := domain.NormalizeSignin(in, false)
	var validation *domain.ValidationError
	if !errors.As(err, &validation) || validation.Key != "error.signin_image_required" {
		t.Fatalf("err = %v, want the missing-image refusal", err)
	}
	if _, err := domain.NormalizeSignin(in, true); err != nil {
		t.Errorf("a stored image must satisfy the rule: %v", err)
	}
	// A submitted image satisfies it too, and is validated on the way in.
	in.Background = signinPNG
	if _, err := domain.NormalizeSignin(in, false); err != nil {
		t.Errorf("a submitted PNG must satisfy the rule: %v", err)
	}
}

func TestValidateSigninBackgroundAcceptsPNGAndJPEGUpToTheCap(t *testing.T) {
	for _, data := range [][]byte{
		signinPNG,
		signinJPEG,
		append(append([]byte{}, signinPNG...), make([]byte, domain.MaxSigninBackgroundBytes-len(signinPNG))...),
	} {
		if err := domain.ValidateSigninBackground(data); err != nil {
			t.Errorf("valid background of %d bytes rejected: %v", len(data), err)
		}
	}
}

func TestValidateSigninBackgroundRejectsWhatItCannotProve(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		key  string
	}{
		{"empty", nil, "error.signin_image_invalid"},
		{"a text file named jpg", []byte("not an image"), "error.signin_image_invalid"},
		{"a gif", append([]byte("GIF89a"), make([]byte, 16)...), "error.signin_image_invalid"},
		{"a png over the cap", append(append([]byte{}, signinPNG...), make([]byte, domain.MaxSigninBackgroundBytes)...), "error.signin_image_too_large"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := domain.ValidateSigninBackground(tc.data)
			var validation *domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("err = %v, want a ValidationError", err)
			}
			if validation.Key != tc.key {
				t.Errorf("key = %q, want %q", validation.Key, tc.key)
			}
		})
	}
}

func TestSigninBackgroundContentTypeFollowsTheMagicBytes(t *testing.T) {
	if got, ok := domain.SigninBackgroundContentType(signinPNG); !ok || got != "image/png" {
		t.Errorf("PNG content type = (%q, %t), want image/png", got, ok)
	}
	if got, ok := domain.SigninBackgroundContentType(signinJPEG); !ok || got != "image/jpeg" {
		t.Errorf("JPEG content type = (%q, %t), want image/jpeg", got, ok)
	}
	if _, ok := domain.SigninBackgroundContentType([]byte("text")); ok {
		t.Error("a text payload was given a content type")
	}
}

// TestDarkenForWhiteTextReachesTheContrastFloor proves the derived overlay: any
// accepted brand colour is darkened until white text on it measures at least
// 4.5:1, and a colour that already passes is returned unchanged.
func TestDarkenForWhiteTextReachesTheContrastFloor(t *testing.T) {
	cases := []string{"#FFFFFF", "#FFFF00", "#8FD3FF", "#C8102E", "#111111", "#7B96FF"}
	for _, hex := range cases {
		if !domain.ValidSigninBrandColor(hex) {
			t.Fatalf("%s must be a valid brand colour", hex)
		}
		darkened := domain.DarkenForWhiteText(hex)
		if !domain.ValidSigninBrandColor(darkened) {
			t.Errorf("darken(%s) = %q, want a #RRGGBB colour", hex, darkened)
		}
		if ratio := domain.ContrastAgainstWhite(darkened); ratio < 4.5 {
			t.Errorf("darken(%s) = %q, contrast %.2f, want at least 4.5", hex, darkened, ratio)
		}
	}
	if got := domain.DarkenForWhiteText("#111111"); got != "#111111" {
		t.Errorf("darken(#111111) = %q, want it unchanged", got)
	}
	if !domain.ValidSigninBrandColor("#c8102e") {
		t.Error("a lower-case colour must be accepted")
	}
	for _, bad := range []string{"", "#C81", "C8102E", "#GGGGGG", "#C8102E ", "rgb(1,2,3)"} {
		if domain.ValidSigninBrandColor(bad) {
			t.Errorf("%q accepted as a brand colour", bad)
		}
	}
}

// TestSigninHighlightsRoundTripThroughJSON proves the stored form of the list:
// one JSON array, decoded defensively so a hand-edited row degrades to "no
// highlights" instead of failing the login page.
func TestSigninHighlightsRoundTripThroughJSON(t *testing.T) {
	highlights := []domain.SigninHighlight{
		{Icon: domain.SigninIconClock, Title: "Four hours", Text: "Or less"},
		{Icon: domain.SigninIconPhone, Title: "24/7"},
	}
	encoded := domain.EncodeSigninHighlights(highlights)
	decoded := domain.DecodeSigninHighlights(encoded)
	if len(decoded) != len(highlights) {
		t.Fatalf("decoded = %+v, want %+v", decoded, highlights)
	}
	for i := range highlights {
		if decoded[i] != highlights[i] {
			t.Errorf("decoded[%d] = %+v, want %+v", i, decoded[i], highlights[i])
		}
	}
	for _, malformed := range []string{"", "not json", `{"icon":"clock"}`, `[{"icon":"rocket"}]`, "[null]"} {
		if got := domain.DecodeSigninHighlights(malformed); got != nil {
			t.Errorf("DecodeSigninHighlights(%q) = %+v, want nil", malformed, got)
		}
	}
	if got := domain.EncodeSigninHighlights(nil); got != "" {
		t.Errorf("EncodeSigninHighlights(nil) = %q, want an empty value", got)
	}
}

// TestDarkenForWhiteTextNeverEchoesAnInvalidColour pins the read-path guard:
// the result lands in a trusted CSS context, so a stored value that is not a
// #RRGGBB colour (a hand-edited row, a future bug) must never come back
// verbatim; it falls back to the dark panel colour instead.
func TestDarkenForWhiteTextNeverEchoesAnInvalidColour(t *testing.T) {
	for _, hostile := range []string{"red;background:url(https://evil.example/x)", "#12345", "", "#GGGGGG", "#315EFF;color:red"} {
		got := domain.DarkenForWhiteText(hostile)
		if got != domain.SigninFallbackPanelColor {
			t.Errorf("domain.DarkenForWhiteText(%q) = %q, want the fallback %q", hostile, got, domain.SigninFallbackPanelColor)
		}
	}
	if got := domain.DarkenForWhiteText("#315EFF"); got == domain.SigninFallbackPanelColor || !domain.ValidSigninBrandColor(got) {
		t.Errorf("a valid colour must still darken to a valid colour, got %q", got)
	}
}
