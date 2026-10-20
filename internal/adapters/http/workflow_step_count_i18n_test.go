package httpadapter

import (
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// A message reference interpolates STRINGS: application.Arg carries a string
// value (application.Text/Value) and MsgValues stringifies every argument, so
// resolveMsg always calls the catalogue sentence with string values. A
// catalogue entry reached through that path that names a numeric verb would
// therefore render "1 field" as "%!d(string=1) field" in every locale. These
// tests pin the copy a builder step card shows and, more generally, that no
// message-rendered surface can print a broken verb.

// TestWorkflowStepFieldCountInterpolates is the focused case: a form step's
// summary counts its fields ("1 field" / "2 fields", "1 campo" / "2 campos")
// in both shipped locales, through the real Msg building and resolution path.
func TestWorkflowStepFieldCountInterpolates(t *testing.T) {
	for _, tc := range []struct{ locale, one, two string }{
		{"en", "1 field", "2 fields"}, {"es", "1 campo", "2 campos"},
	} {
		tr := i18n.New(tc.locale)
		for _, n := range []int{1, 2} {
			def := domain.WorkflowDefinition{
				{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: make([]domain.FormField, n)}},
				{Type: domain.StepResolve},
			}
			got, want := resolveMsg(tr, workflowStepViews(def, 0, nil)[0].Summary), tc.one
			if n == 2 {
				want = tc.two
			}
			if got != want {
				t.Errorf("%s %d-field summary = %q, want %q", tc.locale, n, got, want)
			}
		}
	}
}

// TestMessagesNeverRenderABrokenVerb is the general guard for the same defect
// class: the surfaces whose copy is composed in Go as a message reference
// (the workflow builder's step cards and validation findings, the catalog
// badge, the timeline narrative, the ticket list's unrunnable reason) are
// rendered in both shipped locales and must never contain fmt's bad-verb
// marker, which is what a numeric verb fed a string produces.
func TestMessagesNeverRenderABrokenVerb(t *testing.T) {
	builder := mobileBuilderPageData()
	builder.Draft = append(builder.Draft, domain.WorkflowDefinition{
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "serial", Label: "Serial", Kind: domain.FieldShortText}}}},
		{Type: domain.StepResolve},
	}...)
	builder.Steps = workflowStepViews(builder.Draft, 0, builder.Desks)
	builder.Issues = []domain.WorkflowValidationIssue{
		{Step: 2, Field: "fields", Message: "Step 2: form must have at least one field", Key: "error.workflow.form_fields", Args: []any{2}},
	}

	catalog := fixtureCategoriesIndexData()
	timeline := fixtureDetailData()

	for _, locale := range []string{"en", "es"} {
		for name, body := range map[string]string{
			"workflow builder": renderLocalized(t, locale, "category_workflow", "", builder),
			"catalog":          renderLocalized(t, locale, "categories_index", "categories_background", catalog),
			"timeline":         renderLocalized(t, locale, "tickets_show", "timeline", timeline),
		} {
			if strings.Contains(body, "%!") {
				t.Errorf("%s (%s) rendered a broken format verb: %.600s", name, locale, body)
			}
		}
	}
}
