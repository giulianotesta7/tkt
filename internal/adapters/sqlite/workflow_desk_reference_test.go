package sqlite

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestWorkflowDeskReferences(t *testing.T) {
	for _, mode := range []string{"upsert", "save", "publish", "publish_revision"} {
		for _, order := range []string{"deleted", "saved", "concurrent"} {
			t.Run(mode+"/"+order, func(t *testing.T) {
				s := newTestDB(t)
				ctx := context.Background()
				cat := seedCategory(t, s, "References")
				actor := seedUserRole(t, s, "Editor", "editor@test", true, domain.RoleAdmin)
				desk := seedDeskWithMemberNamed(t, s, actor, "Target")
				ws := newWorkflowStore(s.db)
				initial := []byte(`[]`)
				if _, err := ws.SaveDraftIfRevision(ctx, cat, 0, actor, initial); err != nil {
					t.Fatal(err)
				}
				before, err := ws.GetAttribution(ctx, cat)
				if err != nil {
					t.Fatal(err)
				}
				draft := mustCanon(t, claimDef(desk))
				var writeErr, deleteErr error
				var issues []domain.WorkflowValidationIssue
				var version, revision int64
				write := func() {
					switch mode {
					case "upsert":
						writeErr = ws.UpsertDraft(ctx, cat, draft)
					case "save":
						revision, writeErr = ws.SaveDraftIfRevision(ctx, cat, 1, actor, draft)
					case "publish":
						version, issues, writeErr = ws.Publish(ctx, cat, draft, &actor)
					case "publish_revision":
						version, revision, issues, writeErr = ws.PublishAtRevision(ctx, cat, draft, 1, &actor)
					}
				}
				remove := func() { deleteErr = s.DeskStore().Delete(ctx, desk) }
				switch order {
				case "deleted":
					remove()
					write()
				case "saved":
					write()
					remove()
				default:
					start := make(chan struct{})
					var wg sync.WaitGroup
					wg.Add(2)
					go func() { defer wg.Done(); <-start; write() }()
					go func() { defer wg.Done(); <-start; remove() }()
					close(start)
					wg.Wait()
				}
				got, rev, err := ws.GetDraftWithRevision(ctx, cat)
				if err != nil {
					t.Fatal(err)
				}
				if deleteErr == nil {
					if mode == "upsert" || mode == "save" {
						var validation *domain.ValidationError
						if !errors.As(writeErr, &validation) || validation.Field != "desk_id" || validation.Key != "error.workflow.choose_desk" || validation.Message != "Step 1: choose a desk" {
							t.Errorf("write after deletion = %v, want choose-desk ValidationError", writeErr)
						}
					} else if writeErr != nil || len(issues) != 1 || issues[0].Key != "error.workflow.choose_desk" || issues[0].Message != "Step 1: choose a desk" {
						t.Errorf("publish after deletion = %v / %+v, want choose-desk issue", writeErr, issues)
					}
					if string(got) != string(initial) || rev != 1 || version != 0 || revision != 0 {
						t.Errorf("rejected write changed state: draft=%s rev=%d version=%d returned revision=%d", got, rev, version, revision)
					}
					after, err := ws.GetAttribution(ctx, cat)
					if err != nil || !reflect.DeepEqual(before, after) {
						t.Errorf("rejected write changed attribution: %v", err)
					}
					published, err := ws.GetCurrentVersion(ctx, cat)
					if err != nil || published != nil {
						t.Errorf("rejected write published: %+v %v", published, err)
					}
				} else {
					if !errors.Is(deleteErr, domain.ErrReferenced) || writeErr != nil || len(issues) != 0 || string(got) != string(draft) {
						t.Fatalf("write winner: delete=%v write=%v issues=%v draft=%s", deleteErr, writeErr, issues, got)
					}
					if _, err := s.DeskStore().GetByID(ctx, desk); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestWorkflowDeskReferencesIncompleteDrafts(t *testing.T) {
	for _, guarded := range []bool{false, true} {
		s := newTestDB(t)
		ctx := context.Background()
		cat := seedCategory(t, s, "Incomplete")
		actor := seedUserRole(t, s, "Editor", "editor@test", true, domain.RoleAdmin)
		// Existing but unstaffed desks and unset selections remain valid drafts.
		desk := seedDesk(t, s, "Empty")
		ws := newWorkflowStore(s.db)
		for i, draft := range [][]byte{
			[]byte(`[]`),
			[]byte(`[{"type":"assign_to_desk"}]`),
			mustCanon(t, claimDef(0)),
			mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: desk}}}),
		} {
			var err error
			if guarded {
				_, err = ws.SaveDraftIfRevision(ctx, cat, int64(i), actor, draft)
			} else {
				err = ws.UpsertDraft(ctx, cat, draft)
			}
			if err != nil {
				t.Fatalf("guarded=%v draft=%s: %v", guarded, draft, err)
			}
			got, err := ws.GetDraft(ctx, cat)
			if err != nil || string(got) != string(draft) {
				t.Fatalf("draft roundtrip = %s %v", got, err)
			}
		}
	}
}
