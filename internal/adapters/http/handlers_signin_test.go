package httpadapter

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The Sign-in page section (issue #383, S9/S10): one multipart form that stores
// the panel's text facts and an optional background image in one submit, a
// background image route that mirrors the logo's headers, and a login page that
// renders the defaults when nothing is configured and the stored panel when it
// is — always escaped, never interpreted as markup.

// signinPNGBytes and signinJPEGBytes prove their type by magic bytes.
func signinPNGBytes() []byte { return append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 48)...) }

func signinJPEGBytes() []byte { return append([]byte("\xFF\xD8\xFF\xE0"), make([]byte, 48)...) }

// postSignin submits the sign-in form as multipart, the shape a browser sends.
func postSignin(t *testing.T, h *harness, fields url.Values, image []byte, imageName string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for name, values := range fields {
		for _, value := range values {
			if err := w.WriteField(name, value); err != nil {
				t.Fatalf("write multipart field %q: %v", name, err)
			}
		}
	}
	if image != nil {
		part, err := w.CreateFormFile(signinBackgroundField, imageName)
		if err != nil {
			t.Fatalf("create image part: %v", err)
		}
		if _, err := part.Write(image); err != nil {
			t.Fatalf("write image part: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/settings/signin", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	return rec
}

// storedSignin reads the panel through the store port.
func storedSignin(t *testing.T, h *harness) application.SigninConfiguration {
	t.Helper()
	configuration, err := h.store.SigninStore().GetSignin(context.Background())
	if err != nil {
		t.Fatalf("read stored sign-in panel: %v", err)
	}
	return configuration
}

// defaultSigninForm is a valid submission with the documented defaults.
func defaultSigninForm() url.Values {
	return url.Values{"content": {domain.SigninContentSteps}, "background": {domain.SigninBackgroundDark}}
}

// TestSigninSectionRendersTheDefaults proves the section exists between
// Organization and Language, carries its own form and save bar, and shows the
// default panel in the preview.
func TestSigninSectionRendersTheDefaults(t *testing.T) {
	h := newHarness(t)

	body := sectionGet(t, h, "/settings/signin")
	for _, want := range []string{
		`<h2 class="settings-section-title">Sign-in page</h2>`,
		`action="/settings/signin" enctype="multipart/form-data"`,
		`id="signin_title" name="title"`,
		`id="signin_subtitle" name="subtitle"`,
		`name="content" value="steps" checked`,
		`name="background" value="dark" checked`,
		`id="signin_background" name="background" accept="image/png,image/jpeg"`,
		`name="support_phone"`,
		`name="support_hours"`,
		`Save sign-in page`,
		`data-settings-savebar hidden`,
		`data-preview-panel`,
		`Welcome to Help desk`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the sign-in section must contain %q, got: %s", want, body)
		}
	}
	// The nav order is Organization, Sign-in page, Language (S10).
	organization := strings.Index(body, `href="/settings/organization"`)
	signin := strings.Index(body, `href="/settings/signin"`)
	language := strings.Index(body, `href="/settings/language"`)
	if organization < 0 || signin < 0 || language < 0 || !(organization < signin && signin < language) {
		t.Errorf("the nav must list Organization, Sign-in page, Language in that order")
	}
}

// TestSigninSectionIsGatedOnCapManageSettings proves the section and its write
// routes keep the instance-configuration capability.
func TestSigninSectionIsGatedOnCapManageSettings(t *testing.T) {
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Signin User", "signin-user@tkt.test", domain.RoleUser)
	session := seedSession(t, h.store, requester.ID)

	if rec := getAs(t, h, "/settings/signin", session.ID); rec.Code != http.StatusForbidden {
		t.Errorf("requester GET /settings/signin = %d, want 403", rec.Code)
	}
	rec := h.postFormAs(t, "/settings/signin", url.Values{"title": {"Hijacked"}}, session.ID)
	if rec.Code != http.StatusForbidden {
		t.Errorf("requester POST /settings/signin = %d, want 403", rec.Code)
	}
	remove := h.postFormAs(t, "/settings/signin/background/remove", url.Values{}, session.ID)
	if remove.Code != http.StatusForbidden {
		t.Errorf("requester POST /settings/signin/background/remove = %d, want 403", remove.Code)
	}
	if got := storedSignin(t, h); got.Panel.Title != "" {
		t.Errorf("a denied write stored %q, want nothing", got.Panel.Title)
	}
}

// TestSigninSectionSavesThePanel proves the happy path: one multipart submit
// stores every field and redirects back to the section, and the following GET
// renders it in the form and the preview.
func TestSigninSectionSavesThePanel(t *testing.T) {
	h := newHarness(t)

	form := url.Values{
		"title":             {"Sistemas, a un pedido"},
		"subtitle":          {"Cargá tu pedido y seguí su estado."},
		"content":           {domain.SigninContentHighlights},
		"highlight_0_icon":  {domain.SigninIconClock},
		"highlight_0_title": {"Primera respuesta en 4 h"},
		"highlight_0_text":  {"Los críticos en menos de 30 min."},
		"highlight_1_icon":  {domain.SigninIconPhone},
		"highlight_1_title": {"Guardia 24/7"},
		"notice_enabled":    {"1"},
		"notice_kind":       {domain.SigninNoticeWarning},
		"notice_title":      {"Mantenimiento programado"},
		"notice_text":       {"El sábado de 10 a 12."},
		"background":        {domain.SigninBackgroundBrand},
		"brand_color":       {"#C8102E"},
		"support_phone":     {"Interno 4400"},
		"support_hours":     {"Lun a vie, 8 a 18"},
	}
	rec := postSignin(t, h, form, nil, "")
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/signin")

	stored := storedSignin(t, h)
	if stored.Panel.Title != "Sistemas, a un pedido" || stored.Panel.Subtitle != "Cargá tu pedido y seguí su estado." {
		t.Errorf("stored panel = %+v, want the submitted text", stored.Panel)
	}
	if len(stored.Panel.Highlights) != 2 || stored.Panel.Highlights[0].Icon != domain.SigninIconClock {
		t.Errorf("stored highlights = %+v, want the two submitted rows", stored.Panel.Highlights)
	}
	if !stored.Panel.NoticeEnabled || stored.Panel.NoticeKind != domain.SigninNoticeWarning {
		t.Errorf("stored notice = %+v, want the warning", stored.Panel)
	}
	if stored.Panel.Background != domain.SigninBackgroundBrand || stored.Panel.BrandColor != "#C8102E" {
		t.Errorf("stored background = %q / %q", stored.Panel.Background, stored.Panel.BrandColor)
	}

	body := sectionGet(t, h, "/settings/signin")
	for _, want := range []string{
		`value="Sistemas, a un pedido"`,
		`name="content" value="highlights" checked`,
		`name="background" value="brand" checked`,
		`data-preview-panel`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the section after a save must contain %q, got: %s", want, body)
		}
	}
}

// TestSigninBackgroundUploadAndServe proves the image travels in the same
// submit, is served by its own public route with the logo's headers, and can be
// removed on its own route.
func TestSigninBackgroundUploadAndServe(t *testing.T) {
	h := newHarness(t)

	form := defaultSigninForm()
	form.Set("background", domain.SigninBackgroundImage)
	rec := postSignin(t, h, form, signinJPEGBytes(), "panel.jpg")
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/signin")

	stored := storedSignin(t, h)
	if !bytes.Equal(stored.Background, signinJPEGBytes()) {
		t.Fatalf("stored image is not the submitted one")
	}
	if stored.BackgroundMIME != domain.SigninBackgroundJPEG {
		t.Errorf("stored image type = %q, want image/jpeg", stored.BackgroundMIME)
	}

	// Anonymous: the sign-in page requests it before any session exists.
	req := httptest.NewRequest(http.MethodGet, "/brand/signin-background", nil)
	imageRec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(imageRec, req)
	if imageRec.Code != http.StatusOK {
		t.Fatalf("anonymous GET /brand/signin-background = %d, want 200", imageRec.Code)
	}
	for header, want := range map[string]string{
		"Content-Type":           "image/jpeg",
		"X-Content-Type-Options": "nosniff",
		"Cache-Control":          "public, max-age=86400",
	} {
		if got := imageRec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if !bytes.Equal(imageRec.Body.Bytes(), signinJPEGBytes()) {
		t.Errorf("served image is not the stored one")
	}

	// The section preview requests the image through the shell's own URL.
	body := sectionGet(t, h, "/settings/signin")
	if !strings.Contains(body, `/brand/signin-background?v=`) {
		t.Errorf("the section must preview the stored image, got: %s", body)
	}

	// Removing it is its own route, and the style returns to dark.
	remove := h.postForm(t, "/settings/signin/background/remove", url.Values{}, false)
	wantRedirect(t, remove, http.StatusSeeOther, "/settings/signin")
	after := storedSignin(t, h)
	if after.HasBackground() {
		t.Errorf("the image survived the removal: %d bytes", len(after.Background))
	}
	if after.Panel.Background != domain.SigninBackgroundDark {
		t.Errorf("background style = %q, want dark", after.Panel.Background)
	}
	if rec := h.get(t, "/brand/signin-background", false); rec.Code != http.StatusNotFound {
		t.Errorf("GET /brand/signin-background after removal = %d, want 404", rec.Code)
	}
}

// TestSigninBackgroundRouteIs404WithoutAnImage proves the route answers 404 when
// the instance holds no image, exactly like the logo route.
func TestSigninBackgroundRouteIs404WithoutAnImage(t *testing.T) {
	h := newHarness(t)

	if rec := h.get(t, "/brand/signin-background", false); rec.Code != http.StatusNotFound {
		t.Errorf("GET /brand/signin-background with no image = %d, want 404", rec.Code)
	}
}

// TestSigninRejectionsStoreNothing proves each documented refusal, its status,
// its message and that nothing was written.
func TestSigninRejectionsStoreNothing(t *testing.T) {
	cases := []struct {
		name      string
		form      url.Values
		image     []byte
		wantCode  int
		wantError string
	}{
		{
			name:      "a text file named jpg",
			form:      defaultSigninForm(),
			image:     []byte("this is not an image"),
			wantCode:  http.StatusUnprocessableEntity,
			wantError: "the background must be a PNG or JPEG image",
		},
		{
			name:      "an image background with no image",
			form:      url.Values{"background": {domain.SigninBackgroundImage}},
			wantCode:  http.StatusUnprocessableEntity,
			wantError: "upload an image or choose another background",
		},
		{
			name:      "a notice switched on without a title",
			form:      url.Values{"background": {domain.SigninBackgroundDark}, "notice_enabled": {"1"}},
			wantCode:  http.StatusUnprocessableEntity,
			wantError: "enter the notice title or turn the notice off",
		},
		{
			name:      "an unknown content choice",
			form:      url.Values{"background": {domain.SigninBackgroundDark}, "content": {"gallery"}},
			wantCode:  http.StatusUnprocessableEntity,
			wantError: "choose steps, highlights, or none",
		},
		{
			name:      "a brand background without a colour",
			form:      url.Values{"background": {domain.SigninBackgroundBrand}},
			wantCode:  http.StatusUnprocessableEntity,
			wantError: "the brand color must be a colour like #315EFF",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			seeded := domain.SigninPanel{Title: "Before", Content: domain.SigninContentNone, Background: domain.SigninBackgroundDark}
			if err := h.store.SigninStore().SetSignin(context.Background(), seeded, nil); err != nil {
				t.Fatalf("seed panel: %v", err)
			}

			rec := postSignin(t, h, tc.form, tc.image, "panel.jpg")
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantCode, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, `class="error-banner" role="alert"`) {
				t.Errorf("a rejection must render the error banner, got: %s", body)
			}
			if !strings.Contains(body, tc.wantError) {
				t.Errorf("the rejection must read %q, got: %s", tc.wantError, body)
			}
			if !strings.Contains(body, `action="/settings/signin"`) {
				t.Errorf("a rejection must re-render the sign-in section, got: %s", body)
			}
			if got := storedSignin(t, h); got.Panel.Title != "Before" || got.HasBackground() {
				t.Errorf("stored panel after a rejection = %+v, want the seeded one untouched", got.Panel)
			}
		})
	}
}

// TestSigninOversizeImageIs413 proves the documented status for an image above
// the cap, with nothing stored.
func TestSigninOversizeImageIs413(t *testing.T) {
	h := newHarness(t)
	oversize := append(append([]byte{}, signinPNGBytes()...), make([]byte, domain.MaxSigninBackgroundBytes)...)

	rec := postSignin(t, h, defaultSigninForm(), oversize, "panel.png")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "the background image must be 1 MiB or smaller") {
		t.Errorf("the 413 must state the cap, got: %s", rec.Body.String())
	}
	if got := storedSignin(t, h); got.HasBackground() || got.Panel.Title != "" {
		t.Errorf("an oversized submission stored %+v, want nothing", got.Panel)
	}
}

// TestSigninTitleIsRenderedEscaped proves panel text is never markup: a saved
// script tag renders as text on the login page.
func TestSigninTitleIsRenderedEscaped(t *testing.T) {
	h := newHarness(t)
	hostile := `<script>alert(1)</script>`

	form := defaultSigninForm()
	form.Set("title", hostile)
	form.Set("subtitle", hostile)
	rec := postSignin(t, h, form, nil, "")
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/signin")

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	loginRec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(loginRec, req)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("GET /login = %d, want 200", loginRec.Code)
	}
	body := loginRec.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Errorf("the panel rendered the submitted markup verbatim")
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("the panel must render the submitted text escaped, got: %s", body)
	}
	// The section echoes it escaped too.
	if section := sectionGet(t, h, "/settings/signin"); strings.Contains(section, hostile) {
		t.Errorf("the section echoed the submitted markup verbatim")
	}
}

// TestLoginRendersTheDefaultPanel proves S9's "never empty": an unconfigured
// instance renders the brand, the default title naming the help desk, the
// subtitle, the three steps and no notice.
func TestLoginRendersTheDefaultPanel(t *testing.T) {
	h := newHarness(t)
	setOrganization(t, h, domain.Organization{Name: "Acme Support", HelpDesk: "Service desk", SupportEmail: "support@acme.test"})

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<main class="auth-entry">`,
		`auth-entry__presentation auth-entry__presentation--dark`,
		`<b>Acme Support</b><span>Service desk</span>`,
		`<h2>Welcome to Service desk</h2>`,
		"Everything your team needs to receive, organize, and resolve support requests.",
		`<ol class="auth-entry__steps">`,
		"Received",
		`<li class="on">`,
		"Assigned",
		"Resolved",
		`href="mailto:support@acme.test"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the default panel must contain %q, got: %s", want, body)
		}
	}
	// The inline stylesheet also mentions these class names, so the assertions
	// read markup: a `class="..."` attribute, never a bare selector.
	if strings.Contains(body, `class="auth-entry__notice`) {
		t.Errorf("an unconfigured panel must render no notice, got: %s", body)
	}
	// The setup shell carries the default panel too.
	setup := newEmptyHarness(t)
	setupRec := doRequest(setup.mux, setup.mw, http.MethodGet, "/setup", nil)
	if setupRec.Code != http.StatusOK {
		t.Fatalf("GET /setup = %d, want 200", setupRec.Code)
	}
	if !strings.Contains(setupRec.Body.String(), "<h2>Welcome to Help desk</h2>") {
		t.Errorf("the setup shell must carry the default panel, got: %s", setupRec.Body.String())
	}
}

// TestLoginRendersTheCustomizedPanel proves every customization reaches the
// sign-in page: the title, the highlights, the notice, the footer contact facts
// and the brand background with the derived contrast.
func TestLoginRendersTheCustomizedPanel(t *testing.T) {
	h := newHarness(t)
	setOrganization(t, h, domain.Organization{Name: "Acme", HelpDesk: "Help desk", SupportEmail: "support@acme.test"})
	panel := domain.SigninPanel{
		Title:    "Sistemas, a un pedido",
		Subtitle: "Cargá tu pedido y seguí su estado.",
		Content:  domain.SigninContentHighlights,
		Highlights: []domain.SigninHighlight{
			{Icon: domain.SigninIconClock, Title: "Four hours", Text: "Or less"},
			{Icon: domain.SigninIconShield, Title: "Guarded"},
		},
		NoticeEnabled: true,
		NoticeKind:    domain.SigninNoticeWarning,
		NoticeTitle:   "Maintenance",
		NoticeText:    "Saturday 10-12",
		Background:    domain.SigninBackgroundBrand,
		BrandColor:    "#FFFF00",
		SupportPhone:  "4400",
		SupportHours:  "Mon-Fri",
	}
	if err := h.store.SigninStore().SetSignin(context.Background(), panel, nil); err != nil {
		t.Fatalf("seed panel: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{
		`<h2>Sistemas, a un pedido</h2>`,
		`<p class="auth-entry__sub">Cargá tu pedido y seguí su estado.</p>`,
		`<ul class="auth-entry__highlights">`,
		"Four hours",
		"Guarded",
		`auth-entry__notice auth-entry__notice--warning`,
		"Maintenance",
		"Saturday 10-12",
		`<b>Phone</b><span>4400</span>`,
		`<b>Hours</b><span>Mon-Fri</span>`,
		`auth-entry__presentation--brand`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the customized panel must contain %q, got: %s", want, body)
		}
	}
	// The brand colour is darkened until white text reaches 4.5:1, so the panel
	// never paints the raw yellow.
	if strings.Contains(body, "--signin-brand:#FFFF00") {
		t.Errorf("the panel painted the raw brand colour, want the derived shade")
	}
	if !strings.Contains(body, domain.DarkenForWhiteText("#FFFF00")) {
		t.Errorf("the panel must paint the derived shade, got: %s", body)
	}
	if strings.Contains(body, `class="auth-entry__steps"`) {
		t.Errorf("a highlights panel must not render the default steps")
	}
}

// TestLoginPanelOmitsAnEmptyMiddleZone proves "none" renders no middle content
// and the panel still carries its brand, title and subtitle.
func TestLoginPanelOmitsAnEmptyMiddleZone(t *testing.T) {
	h := newHarness(t)
	if err := h.store.SigninStore().SetSignin(context.Background(), domain.SigninPanel{
		Title: "Estamos para ayudarte.", Content: domain.SigninContentNone, Background: domain.SigninBackgroundDark,
	}, nil); err != nil {
		t.Fatalf("seed panel: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	body := rec.Body.String()
	if strings.Contains(body, `class="auth-entry__steps"`) || strings.Contains(body, `class="auth-entry__highlights"`) {
		t.Errorf("a none panel must render no middle content, got: %s", body)
	}
	if !strings.Contains(body, "<h2>Estamos para ayudarte.</h2>") {
		t.Errorf("a none panel must keep its title, got: %s", body)
	}
}

// TestSigninSectionRendersThePreviewHooks proves the live preview the form
// mirrors is server-rendered, so it is correct before any script runs.
func TestSigninSectionRendersThePreviewHooks(t *testing.T) {
	h := newHarness(t)

	body := sectionGet(t, h, "/settings/signin")
	for _, want := range []string{
		`data-preview-panel`,
		`data-preview-title-text`,
		`data-preview-subtitle-text`,
		`data-preview-notice`,
		`data-preview-zone="steps"`,
		`data-preview-zone="highlights"`,
		`data-preview-phone-block`,
		`data-preview-hours-block`,
		`data-preview-hl-title="0"`,
		`data-preview-hl-row="0"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the preview must carry %q, got: %s", want, body)
		}
	}
}

// TestSigninEditorShowsOnlyTheFieldsOfTheChosenOptions pins the progressive
// disclosure the maintainer asked for: every dependent block carries the rule
// settings.js applies on each change, and the server renders the initial
// state from what is stored, so a reload never flashes a hidden field.
func TestSigninEditorShowsOnlyTheFieldsOfTheChosenOptions(t *testing.T) {
	h := newHarness(t)
	block := func(body, rule string) string {
		i := strings.Index(body, `data-show-when="`+rule+`"`)
		if i < 0 {
			t.Fatalf("no block for %q", rule)
		}
		return body[i : i+strings.Index(body[i:], ">")]
	}
	rules := []string{"content=highlights", "notice_enabled=checked", "background=brand", "background=image"}

	// Defaults: steps, notice off, dark background: every dependent block hidden.
	body := h.get(t, "/settings/signin", false).Body.String()
	for _, rule := range rules {
		if !strings.Contains(block(body, rule), " hidden") {
			t.Errorf("default: %q must start hidden: %s", rule, block(body, rule))
		}
	}

	// Highlights, notice on and a brand background: those three show, the
	// image field stays hidden.
	form := url.Values{
		"content": {domain.SigninContentHighlights}, "highlight_0_icon": {domain.SigninIconClock}, "highlight_0_title": {"Fast"},
		"notice_enabled": {"1"}, "notice_kind": {domain.SigninNoticeInfo}, "notice_title": {"Heads up"},
		"background": {domain.SigninBackgroundBrand}, "brand_color": {"#C8102E"},
	}
	wantRedirect(t, postSignin(t, h, form, nil, ""), http.StatusSeeOther, "/settings/signin")
	body = h.get(t, "/settings/signin", false).Body.String()
	for _, rule := range []string{"content=highlights", "notice_enabled=checked", "background=brand"} {
		if strings.Contains(block(body, rule), " hidden") {
			t.Errorf("saved: %q must show: %s", rule, block(body, rule))
		}
	}
	if !strings.Contains(block(body, "background=image"), " hidden") {
		t.Errorf("a brand background must hide the image field")
	}
}
