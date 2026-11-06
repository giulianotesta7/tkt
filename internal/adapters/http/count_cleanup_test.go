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
	t.Run("folders", func(t *testing.T) {
		body := renderGolden(t, "categories_index", "", fixtureCategoriesIndexData(), false)
		if strings.Contains(body, "1 folder · 2 categories") || strings.Contains(body, `>Empty</span>`) {
			t.Error("folder counts or empty label remain")
		}
	})
}
