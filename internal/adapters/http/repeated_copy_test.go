package httpadapter

import (
	"strconv"
	"strings"
	"testing"
)

// TestRepeatedCopyIsDeleted covers issue #380 for the screens whose subtitle or
// help line said nothing the same screen did not already say. Each assertion is
// that the retired sentence renders nowhere on the page it belonged to, so a
// later change cannot quietly bring it back.
//
// The ticket form, the two-step catalog and the workflow editor carry their own
// assertions next to the behaviour they belong to (golden_test.go,
// handlers_create_mode_test.go, handlers_category_workflows_test.go), and the
// settings email section's is in handlers_settings_tips_test.go.
func TestRepeatedCopyIsDeleted(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Repeated copy")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	operator := h.createUser(t, "Rita Requester", "rita@example.com", "SuperSecret42!")
	_, rootSession := seedRoot(t, h)

	for _, tc := range []struct {
		screen string
		path   string
		absent []string
	}{
		{
			// The heading says "Preferences"; the section heads say the rest.
			screen: "preferences",
			path:   "/preferences",
			absent: []string{"Personal settings", "A copy of every notification goes to your email address."},
		},
		{
			// The page shows the Department, Desk and Category tree itself.
			screen: "categories",
			path:   "/categories",
			absent: []string{"Manage the Department, Desk, and Category hierarchy"},
		},
		{
			// The heading and the step list name the job.
			screen: "category workflow",
			path:   "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow",
			absent: []string{"Build one ordered path for this category."},
		},
		{
			// The heading names the surface and the cards name their numbers.
			screen: "ticket metrics",
			path:   "/tickets/metrics",
			absent: []string{"Operational workload and resolution"},
		},
		{
			screen: "new user",
			path:   "/users/new",
			absent: []string{"Create an active operator"},
		},
		{
			screen: "edit user",
			path:   "/users/" + strconv.FormatInt(operator.ID, 10) + "/edit",
			absent: []string{"Update operator details"},
		},
	} {
		body := h.get(t, tc.path, false).Body.String()
		for _, gone := range tc.absent {
			if strings.Contains(body, gone) {
				t.Errorf("the %s screen must not carry the repeated copy %q, got: %.1200s", tc.screen, gone, body)
			}
		}
	}

	// The SMTP server's address is what its label says; the field keeps the label
	// and the input, and loses only the toggletip that repeated the name.
	email := sectionBodyFor(t, h, "/settings/email", rootSession.ID)
	for _, gone := range []string{"Host name or IP address of the company&#39;s mail server.", `id="help-tip-email-host"`} {
		if strings.Contains(email, gone) {
			t.Errorf("the email settings section must not carry %q, got: %.1200s", gone, email)
		}
	}
	for _, want := range []string{`<label for="email_host">SMTP server</label>`, `id="email_host" name="email_host"`} {
		if !strings.Contains(email, want) {
			t.Errorf("the SMTP server field must keep %q, got: %.1200s", want, email)
		}
	}
}
