package httpadapter

import (
	"fmt"
	"github.com/giulianotesta7/tkt/internal/domain"
	"testing"
)

// S4/S6/S12: discovery excludes both empty-desk categories; creation differs.
func TestAPIRegressionEmptyDeskCreation(t *testing.T) {
	for _, strategy := range []domain.AssignmentStrategy{domain.StrategyClaim, domain.StrategyLeastLoaded} {
		t.Run(string(strategy), func(t *testing.T) {
			f := newAPIFixture(t)
			_, token := apiWriteKey(t, f, f.user, "catalog:read", "tickets:write")
			desk, err := f.h.desks.Create(t.Context(), *f.h.admin, "Empty after publication")
			if err != nil {
				t.Fatal(err)
			}
			f.h.publishWorkflow(t, f.h.bugCategory.ID, domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: desk.ID, Strategy: strategy}}})
			if err := f.h.desks.RemoveMember(t.Context(), *f.h.admin, desk.ID, f.h.admin.ID); err != nil {
				t.Fatal(err)
			}
			list := apiObject(t, f.request("GET", "/api/v1/categories", "Bearer "+token, "", ""))
			if len(list["items"].([]any)) != 0 {
				t.Fatal("unrunnable category offered")
			}
			before := probeBytes(t, f)
			counts := f.domainCounts(t)
			body := fmt.Sprintf(`{"category_id":%d,"title":"Empty desk","priority":"medium"}`, f.h.bugCategory.ID)
			w := f.request("POST", "/api/v1/tickets", "Bearer "+token, body, "application/json")
			if strategy == domain.StrategyLeastLoaded {
				assertAPIProblem(t, w, problemConflict, "/api/v1/tickets")
				if f.domainCounts(t) != counts || probeBytes(t, f) != before {
					t.Fatal("failed automatic assignment wrote data")
				}
				return
			}
			item := apiWriteObject(t, w, 201)
			if item["assignee"] != nil || item["state"] != "new" || f.domainCounts(t)[0] != counts[0]+1 {
				t.Fatalf("claim receipt=%v", item)
			}
		})
	}
}
