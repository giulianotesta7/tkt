package httpadapter

import (
	"strings"
	"testing"
)

func TestCountCleanup(t *testing.T) {
	t.Run("users", func(t *testing.T) {
		body := renderGolden(t, "users_index", "", fixtureUsersIndexData(), false)
		if strings.Contains(body, " accounts</p>") {
			t.Error("duplicate user total remains")
		}
		if !strings.Contains(body, `class="users-status-tabs"`) {
			t.Error("user filters missing")
		}
	})
	t.Run("workflow", func(t *testing.T) {
		body := renderGolden(t, "category_workflow", "", mobileBuilderPageData(), false)
		if strings.Contains(body, `class="workflow-pane-count"`) {
			t.Error("workflow step count remains")
		}
		if !strings.Contains(body, `class="workflow-step-rail"`) {
			t.Error("workflow step rail missing")
		}
	})
	t.Run("departments", func(t *testing.T) {
		body := renderGolden(t, "categories_index", "", fixtureCategoriesIndexData(), false)
		if strings.Contains(body, "2 desks · 3 agents") {
			t.Error("department agent count remains")
		}
		if !strings.Contains(body, ">2 desks</span>") || !strings.Contains(body, ">3 agents</span>") {
			t.Error("desk count or desk agent count missing")
		}
	})
}
