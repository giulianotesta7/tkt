package sqlite

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestDeskStoreDeleteWorkflowReferences(t *testing.T) {
	for _, source := range []string{"draft", "published", "superseded", "pinned"} {
		t.Run(source, func(t *testing.T) {
			s := newTestDB(t)
			ctx := context.Background()
			cat := seedCategory(t, s, "workflow")
			agent := seedUserRole(t, s, "Agent", "agent@test", true, domain.RoleAgent)
			desk := seedDeskWithMemberNamed(t, s, agent, "Protected")
			ws := s.WorkflowStore()
			def := claimDef(desk)
			bytes := mustCanon(t, def)
			if source == "draft" {
				if err := ws.UpsertDraft(ctx, cat, bytes); err != nil {
					t.Fatal(err)
				}
			} else {
				vid, issues, err := ws.Publish(ctx, cat, bytes, nil)
				if err != nil || len(issues) != 0 {
					t.Fatalf("publish: %v %v", issues, err)
				}
				if source == "pinned" {
					in := buildCreateInput(cat, vid, agent, def, nil, 0, "active", domain.StateNew, nil)
					if _, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(ctx, in); err != nil {
						t.Fatal(err)
					}
				}
				plain := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "work"}}})
				if source == "published" {
					if err := ws.UpsertDraft(ctx, cat, plain); err != nil {
						t.Fatal(err)
					}
				} else if _, issues, err := ws.Publish(ctx, cat, plain, nil); err != nil || len(issues) != 0 {
					t.Fatalf("supersede: %v %v", issues, err)
				}
			}
			before, revision, err := ws.(application.WorkflowDraftRevisionStore).GetDraftWithRevision(ctx, cat)
			if err != nil {
				t.Fatal(err)
			}
			current, err := s.WorkflowVersionStore().GetCurrentVersion(ctx, cat)
			if err != nil {
				t.Fatal(err)
			}
			assertDeskDeleteReferenced(t, s, desk)
			after, afterRevision, err := ws.(application.WorkflowDraftRevisionStore).GetDraftWithRevision(ctx, cat)
			if err != nil || string(before) != string(after) || revision != afterRevision {
				t.Fatalf("draft changed: %s/%d -> %s/%d (%v)", before, revision, after, afterRevision, err)
			}
			afterCurrent, err := s.WorkflowVersionStore().GetCurrentVersion(ctx, cat)
			if err != nil || !reflect.DeepEqual(current, afterCurrent) {
				t.Fatalf("published version changed: %v", err)
			}
		})
	}
}

func assertDeskDeleteReferenced(t *testing.T, s *Store, id int64) {
	t.Helper()
	ctx := context.Background()
	before, err := s.DeskStore().GetByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	members, err := s.DeskStore().ListMembers(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	err = s.DeskStore().Delete(ctx, id)
	var ref *domain.ReferencedError
	if !errors.As(err, &ref) || ref.Kind != "desk" || ref.ID != id {
		t.Errorf("delete = %v, want desk ReferencedError ID %d", err, id)
	}
	after, err := s.DeskStore().GetByID(ctx, id)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Errorf("desk changed after refusal: %v", err)
	}
	afterMembers, err := s.DeskStore().ListMembers(ctx, id)
	if err != nil || !reflect.DeepEqual(members, afterMembers) {
		t.Errorf("members changed after refusal: %v", err)
	}
}

func TestDeskStoreDeleteAssignmentHistory(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	desk := seedDesk(t, s, "Historical")
	cat := seedCategory(t, s, "legacy")
	tk := seedTicket(t, s, domain.Ticket{Number: 1, Title: "Historical", CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateClosed, CreatedAt: testClock, UpdatedAt: testClock})
	// No workflow definition references this desk: the audit alone protects it.
	event := domain.AuditEvent{TicketID: tk.ID, Actor: "workflow", Action: domain.ActionWorkflowAssignment, DeskID: &desk, Field: ptr("user"), ToValue: ptr("1"), CreatedAt: testClock}
	if err := s.AuditStore().Append(ctx, event); err != nil {
		t.Fatal(err)
	}
	before, err := s.AuditStore().ListByTicket(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertDeskDeleteReferenced(t, s, desk)
	after, err := s.AuditStore().ListByTicket(ctx, tk.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("assignment history changed: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestDeskStoreDeleteUnreferencedAndNotFound(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	desk := seedDesk(t, s, "Unused")
	cat := seedCategory(t, s, "other")
	// Neither a longer numeric ID nor a textual mention is a reference.
	for i := desk + 1; i <= desk*10; i++ {
		seedDesk(t, s, fmt.Sprintf("Other %d", i))
	}
	def := claimDef(desk * 10)
	def = append(def, domain.WorkflowStep{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: fmt.Sprintf("desk_id: %d", desk)}})
	if err := s.WorkflowStore().UpsertDraft(ctx, cat, mustCanon(t, def)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeskStore().Delete(ctx, desk); err != nil {
		t.Fatalf("delete unreferenced: %v", err)
	}
	if _, err := s.DeskStore().GetByID(ctx, desk); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted desk: %v", err)
	}
	var nf *domain.NotFoundError
	if err := s.DeskStore().Delete(ctx, desk); !errors.As(err, &nf) || nf.Kind != "desk" || nf.ID != desk {
		t.Fatalf("delete absent: %v", err)
	}
}
