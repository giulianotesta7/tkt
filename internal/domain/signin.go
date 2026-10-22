package domain

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// Sign-in panel customization (issue #383, S9/S10). The two-column sign-in page
// carries a dark presentation panel, and an administrator may fill it from
// Settings. Everything here is plain text with a documented limit and a closed
// set where the interface offers a choice: the panel is rendered escaped, so no
// value is ever interpreted as markup, and a value outside a closed set is
// refused instead of silently replaced.
const (
	// MaxSigninTitleRunes bounds the panel title.
	MaxSigninTitleRunes = 80
	// MaxSigninSubtitleRunes bounds the panel subtitle.
	MaxSigninSubtitleRunes = 200
	// MaxSigninHighlights bounds the custom middle content.
	MaxSigninHighlights = 3
	// MaxSigninHighlightTitleRunes and MaxSigninHighlightTextRunes bound one
	// highlight row.
	MaxSigninHighlightTitleRunes = 60
	MaxSigninHighlightTextRunes  = 120
	// MaxSigninNoticeTitleRunes and MaxSigninNoticeTextRunes bound the notice.
	MaxSigninNoticeTitleRunes = 80
	MaxSigninNoticeTextRunes  = 240
	// MaxSigninSupportPhoneRunes and MaxSigninSupportHoursRunes bound the two
	// contact facts the panel footer adds beside Organization's email.
	MaxSigninSupportPhoneRunes = 40
	MaxSigninSupportHoursRunes = 60
	// MaxSigninBackgroundBytes is the background image cap: a photograph behind
	// a dark overlay, so a mebibyte is generous and bounds one upload.
	MaxSigninBackgroundBytes = 1 << 20
)

// The closed sets the panel stores. An empty value means the documented
// default, so a hand-edited row still renders a usable panel.
const (
	// SigninContentSteps renders the three-step journey (the default),
	// SigninContentHighlights the administrator's own rows, and
	// SigninContentNone nothing between the subtitle and the footer.
	SigninContentSteps      = "steps"
	SigninContentHighlights = "highlights"
	SigninContentNone       = "none"

	// SigninNoticeInfo and SigninNoticeWarning are the two notice kinds.
	SigninNoticeInfo    = "info"
	SigninNoticeWarning = "warning"

	// SigninBackgroundDark is the panel's own dark surface (the default),
	// SigninBackgroundBrand a colour the administrator picks, and
	// SigninBackgroundImage an uploaded photograph under a fixed dark overlay.
	SigninBackgroundDark  = "dark"
	SigninBackgroundBrand = "brand"
	SigninBackgroundImage = "image"

	// The highlight icons: an inline SVG each, never an emoji, so the mark
	// renders the same on every platform.
	SigninIconClock  = "clock"
	SigninIconPhone  = "phone"
	SigninIconCheck  = "check"
	SigninIconShield = "shield"
	SigninIconChat   = "chat"
	SigninIconInfo   = "info"

	// SigninBackgroundPNG and SigninBackgroundJPEG are the two proven types a
	// background image may be, and the types it is served as.
	SigninBackgroundPNG  = "image/png"
	SigninBackgroundJPEG = "image/jpeg"
)

// English user-facing panel messages (D5: the domain is their single source).
// Each one is also the English catalogue entry a rejected form shows.
const (
	ErrMsgSigninTitleInvalid        = "the title must fit on one line and have at most 80 characters"
	ErrMsgSigninSubtitleInvalid     = "the subtitle must fit on one line and have at most 200 characters"
	ErrMsgSigninContentInvalid      = "choose steps, highlights, or none"
	ErrMsgSigninHighlightsTooMany   = "at most 3 highlights"
	ErrMsgSigninHighlightInvalid    = "every highlight needs an icon from the list and a title of at most 60 characters"
	ErrMsgSigninNoticeTitleRequired = "enter the notice title or turn the notice off"
	ErrMsgSigninNoticeInvalid       = "the notice needs an info or warning kind and at most 240 characters of text"
	ErrMsgSigninBackgroundInvalid   = "choose dark, brand color, or image"
	ErrMsgSigninBrandColorInvalid   = "the brand color must be a colour like #315EFF"
	ErrMsgSigninImageRequired       = "upload an image or choose another background"
	ErrMsgSigninImageInvalid        = "the background must be a PNG or JPEG image"
	ErrMsgSigninImageTooLarge       = "the background image must be 1 MiB or smaller"
	ErrMsgSigninContactInvalid      = "the support phone must fit on one line and have at most 40 characters, and the hours at most 60"
)

// SigninHighlight is one row of the custom middle content: an icon from the
// closed set, a title and a short text.
type SigninHighlight struct {
	Icon  string `json:"icon"`
	Title string `json:"title"`
	Text  string `json:"text,omitempty"`
}

// SigninPanel is the stored panel configuration. Empty text fields mean "use
// the catalogue default", which is why the renderer — not this value — decides
// the copy an unconfigured instance shows.
type SigninPanel struct {
	Title    string
	Subtitle string
	// Content is one of the three SigninContent* values.
	Content    string
	Highlights []SigninHighlight
	// NoticeEnabled decides whether the notice renders at all; a disabled
	// notice stores no text.
	NoticeEnabled bool
	NoticeKind    string
	NoticeTitle   string
	NoticeText    string
	// Background is one of the three SigninBackground* values.
	Background   string
	BrandColor   string
	SupportPhone string
	SupportHours string
}

// SigninInput is one submission of the sign-in page form. Background carries a
// newly uploaded image (nil keeps the stored one), and RemoveBackground drops
// it, so the three states a file field can have are distinguishable.
type SigninInput struct {
	Panel            SigninPanel
	Background       []byte
	RemoveBackground bool
}

// SigninImage returns the background image the panel would have after this
// submission, or nil when it would have none.
func SigninImage(in SigninInput) []byte {
	if in.RemoveBackground {
		return nil
	}
	return in.Background
}

// SigninContentOptions, SigninNoticeKinds, SigninBackgroundOptions and
// SigninIconOptions list the closed sets in the order the form offers them.
func SigninContentOptions() []string {
	return []string{SigninContentSteps, SigninContentHighlights, SigninContentNone}
}

func SigninNoticeKinds() []string { return []string{SigninNoticeInfo, SigninNoticeWarning} }

func SigninBackgroundOptions() []string {
	return []string{SigninBackgroundDark, SigninBackgroundBrand, SigninBackgroundImage}
}

func SigninIconOptions() []string {
	return []string{SigninIconClock, SigninIconPhone, SigninIconCheck, SigninIconShield, SigninIconChat, SigninIconInfo}
}

// SigninIconLabelKey is the catalogue key of one icon's label in the picker.
func SigninIconLabelKey(icon string) string { return "signin.icon." + icon }

// NormalizeSignin trims and validates one submission and returns the panel to
// store. hasStoredImage says whether the instance already holds a background
// image, which is what makes "image" a valid choice without a new upload. Every
// refusal names the field it belongs to and the catalogue sentence it renders.
func NormalizeSignin(in SigninInput, hasStoredImage bool) (SigninPanel, error) {
	out := SigninPanel{
		Title:        strings.TrimSpace(in.Panel.Title),
		Subtitle:     strings.TrimSpace(in.Panel.Subtitle),
		Content:      strings.TrimSpace(in.Panel.Content),
		Background:   strings.TrimSpace(in.Panel.Background),
		BrandColor:   strings.ToUpper(strings.TrimSpace(in.Panel.BrandColor)),
		SupportPhone: strings.TrimSpace(in.Panel.SupportPhone),
		SupportHours: strings.TrimSpace(in.Panel.SupportHours),
	}
	switch {
	case utf8.RuneCountInString(out.Title) > MaxSigninTitleRunes || strings.ContainsFunc(out.Title, isLineBreakOrControl):
		return SigninPanel{}, &ValidationError{Field: "title", Message: ErrMsgSigninTitleInvalid, Key: "error.signin_title_invalid"}
	case utf8.RuneCountInString(out.Subtitle) > MaxSigninSubtitleRunes || strings.ContainsFunc(out.Subtitle, isLineBreakOrControl):
		return SigninPanel{}, &ValidationError{Field: "subtitle", Message: ErrMsgSigninSubtitleInvalid, Key: "error.signin_subtitle_invalid"}
	}

	if out.Content == "" {
		out.Content = SigninContentSteps
	}
	if !isSigninContent(out.Content) {
		return SigninPanel{}, &ValidationError{Field: "content", Message: ErrMsgSigninContentInvalid, Key: "error.signin_content_invalid"}
	}
	highlights, err := normalizeSigninHighlights(in.Panel.Highlights)
	if err != nil {
		return SigninPanel{}, err
	}
	out.Highlights = highlights

	if in.Panel.NoticeEnabled {
		out.NoticeEnabled = true
		out.NoticeKind = strings.TrimSpace(in.Panel.NoticeKind)
		if out.NoticeKind == "" {
			out.NoticeKind = SigninNoticeInfo
		}
		out.NoticeTitle = strings.TrimSpace(in.Panel.NoticeTitle)
		out.NoticeText = strings.TrimSpace(in.Panel.NoticeText)
		switch {
		case out.NoticeTitle == "":
			return SigninPanel{}, &ValidationError{Field: "notice", Message: ErrMsgSigninNoticeTitleRequired, Key: "error.signin_notice_title_required"}
		case !isSigninNoticeKind(out.NoticeKind),
			utf8.RuneCountInString(out.NoticeTitle) > MaxSigninNoticeTitleRunes,
			utf8.RuneCountInString(out.NoticeText) > MaxSigninNoticeTextRunes,
			strings.ContainsFunc(out.NoticeTitle+out.NoticeText, isLineBreakOrControl):
			return SigninPanel{}, &ValidationError{Field: "notice", Message: ErrMsgSigninNoticeInvalid, Key: "error.signin_notice_invalid"}
		}
	}

	if out.Background == "" {
		out.Background = SigninBackgroundDark
	}
	if !isSigninBackground(out.Background) {
		return SigninPanel{}, &ValidationError{Field: "background", Message: ErrMsgSigninBackgroundInvalid, Key: "error.signin_background_invalid"}
	}
	switch {
	case out.BrandColor != "" && !ValidSigninBrandColor(out.BrandColor):
		return SigninPanel{}, &ValidationError{Field: "brand_color", Message: ErrMsgSigninBrandColorInvalid, Key: "error.signin_brand_color_invalid"}
	case out.Background == SigninBackgroundBrand && out.BrandColor == "":
		return SigninPanel{}, &ValidationError{Field: "brand_color", Message: ErrMsgSigninBrandColorInvalid, Key: "error.signin_brand_color_invalid"}
	}

	if in.Background != nil {
		if err := ValidateSigninBackground(in.Background); err != nil {
			return SigninPanel{}, err
		}
	}
	hasImage := !in.RemoveBackground && (len(in.Background) > 0 || hasStoredImage)
	if out.Background == SigninBackgroundImage && !hasImage {
		return SigninPanel{}, &ValidationError{Field: "background", Message: ErrMsgSigninImageRequired, Key: "error.signin_image_required"}
	}

	if utf8.RuneCountInString(out.SupportPhone) > MaxSigninSupportPhoneRunes ||
		strings.ContainsFunc(out.SupportPhone, isLineBreakOrControl) {
		return SigninPanel{}, &ValidationError{Field: "support_phone", Message: ErrMsgSigninContactInvalid, Key: "error.signin_contact_invalid"}
	}
	if utf8.RuneCountInString(out.SupportHours) > MaxSigninSupportHoursRunes ||
		strings.ContainsFunc(out.SupportHours, isLineBreakOrControl) {
		return SigninPanel{}, &ValidationError{Field: "support_hours", Message: ErrMsgSigninContactInvalid, Key: "error.signin_contact_invalid"}
	}
	return out, nil
}

// normalizeSigninHighlights keeps the rows that carry something, refuses more
// than the documented number, and validates each kept row: an untouched "add a
// highlight" row must not block a save, but a row with a title must be usable.
func normalizeSigninHighlights(rows []SigninHighlight) ([]SigninHighlight, error) {
	kept := make([]SigninHighlight, 0, len(rows))
	for _, row := range rows {
		highlight := SigninHighlight{
			Icon:  strings.TrimSpace(row.Icon),
			Title: strings.TrimSpace(row.Title),
			Text:  strings.TrimSpace(row.Text),
		}
		// The form always submits an icon for every row it renders, so a row with
		// no title and no text is an untouched "add a highlight" slot, not a row
		// the administrator filled in.
		if highlight.Title == "" && highlight.Text == "" {
			continue
		}
		kept = append(kept, highlight)
	}
	if len(kept) > MaxSigninHighlights {
		return nil, &ValidationError{Field: "highlights", Message: ErrMsgSigninHighlightsTooMany, Key: "error.signin_highlights_too_many"}
	}
	for _, highlight := range kept {
		if !isSigninIcon(highlight.Icon) ||
			highlight.Title == "" ||
			utf8.RuneCountInString(highlight.Title) > MaxSigninHighlightTitleRunes ||
			utf8.RuneCountInString(highlight.Text) > MaxSigninHighlightTextRunes ||
			strings.ContainsFunc(highlight.Title+highlight.Text, isLineBreakOrControl) {
			return nil, &ValidationError{Field: "highlights", Message: ErrMsgSigninHighlightInvalid, Key: "error.signin_highlight_invalid"}
		}
	}
	return kept, nil
}

// ValidateSigninBackground accepts a PNG or JPEG background of at most the
// documented cap. The type is proven by the magic bytes, never by a claimed
// name: the bytes are served back as an image behind the panel.
func ValidateSigninBackground(data []byte) error {
	switch {
	case len(data) > MaxSigninBackgroundBytes:
		return &ValidationError{Field: "background_image", Message: ErrMsgSigninImageTooLarge, Key: "error.signin_image_too_large"}
	case len(data) == 0, !IsPNG(data) && !IsJPEG(data):
		return &ValidationError{Field: "background_image", Message: ErrMsgSigninImageInvalid, Key: "error.signin_image_invalid"}
	}
	return nil
}

// SigninBackgroundContentType answers the type of a validated background: the
// one its magic bytes prove, so the route serves what the row holds.
func SigninBackgroundContentType(data []byte) (string, bool) {
	switch {
	case IsPNG(data):
		return SigninBackgroundPNG, true
	case IsJPEG(data):
		return SigninBackgroundJPEG, true
	default:
		return "", false
	}
}

// SigninFallbackPanelColor is the dark panel colour a brand background falls
// back to when the stored brand colour is not a valid #RRGGBB value.
const SigninFallbackPanelColor = "#111111"

// ValidSigninBrandColor reports whether hex is a six-digit #RRGGBB colour.
func ValidSigninBrandColor(hex string) bool {
	if len(hex) != 7 || hex[0] != '#' {
		return false
	}
	for _, r := range hex[1:] {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// DarkenForWhiteText answers the colour the panel paints for a brand background:
// the administrator's colour, darkened only as far as white text needs to reach
// the 4.5:1 contrast floor. A colour that already passes is returned unchanged,
// so the panel shows the brand's own shade whenever it can.
func DarkenForWhiteText(hex string) string {
	// The result is written into a trusted CSS context, so a value that is
	// not a #RRGGBB colour never comes back verbatim, whatever is stored.
	if !ValidSigninBrandColor(hex) {
		return SigninFallbackPanelColor
	}
	r, g, b := parseHexChannels(hex)
	for i := 0; i < 32 && contrastAgainstWhite(r, g, b) < 4.5; i++ {
		// Scale every channel by 92 percent: a deterministic, monotone darkening
		// that reaches black long before the loop bound.
		r, g, b = r*92/100, g*92/100, b*92/100
	}
	return fmt.Sprintf("#%02X%02X%02X", r, g, b)
}

// ContrastAgainstWhite measures the WCAG contrast ratio of white text on hex.
func ContrastAgainstWhite(hex string) float64 {
	if !ValidSigninBrandColor(hex) {
		return 0
	}
	r, g, b := parseHexChannels(hex)
	return contrastAgainstWhite(r, g, b)
}

func contrastAgainstWhite(r, g, b int) float64 {
	luminance := relativeLuminance(r, g, b)
	return 1.05 / (luminance + 0.05)
}

// relativeLuminance is the WCAG 2.x relative luminance of one sRGB colour.
func relativeLuminance(r, g, b int) float64 {
	channel := func(v int) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(r) + 0.7152*channel(g) + 0.0722*channel(b)
}

func parseHexChannels(hex string) (int, int, int) {
	channel := func(i int) int {
		var v int
		_, _ = fmt.Sscanf(hex[1+i*2:1+i*2+2], "%02x", &v)
		return v
	}
	return channel(0), channel(1), channel(2)
}

// EncodeSigninHighlights stores the highlighted rows as one JSON array. An
// empty list stores the empty string, so an instance with no highlights holds no
// row at all.
func EncodeSigninHighlights(rows []SigninHighlight) string {
	if len(rows) == 0 {
		return ""
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		// A slice of strings cannot fail to marshal; a failure would be a
		// programming error, not a runtime condition.
		return ""
	}
	return string(encoded)
}

// DecodeSigninHighlights reads the stored array defensively: a malformed value,
// an unknown icon, or a row that cannot render degrades to "no highlights"
// instead of failing the login page.
func DecodeSigninHighlights(stored string) []SigninHighlight {
	if strings.TrimSpace(stored) == "" {
		return nil
	}
	var rows []SigninHighlight
	if err := json.Unmarshal([]byte(stored), &rows); err != nil {
		return nil
	}
	out := make([]SigninHighlight, 0, len(rows))
	for _, row := range rows[:min(len(rows), MaxSigninHighlights)] {
		if !isSigninIcon(row.Icon) || strings.TrimSpace(row.Title) == "" {
			continue
		}
		out = append(out, SigninHighlight{
			Icon:  row.Icon,
			Title: strings.TrimSpace(row.Title),
			Text:  strings.TrimSpace(row.Text),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isSigninContent(value string) bool {
	for _, option := range SigninContentOptions() {
		if value == option {
			return true
		}
	}
	return false
}

func isSigninNoticeKind(value string) bool {
	for _, option := range SigninNoticeKinds() {
		if value == option {
			return true
		}
	}
	return false
}

func isSigninBackground(value string) bool {
	for _, option := range SigninBackgroundOptions() {
		if value == option {
			return true
		}
	}
	return false
}

func isSigninIcon(value string) bool {
	for _, option := range SigninIconOptions() {
		if value == option {
			return true
		}
	}
	return false
}
