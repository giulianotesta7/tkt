package sqlite

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Attachment persistence (issue #366, S3/S4): attachments are BLOB rows of
// migration 0025, inserted ONLY by the units of work that create a ticket or a
// comment, inside their transaction; AttachmentStore reads them, filtering the
// files of internal comments for a reader without that visibility.

func attachmentFixture(name string, data []byte, uploader int64, at time.Time) domain.Attachment {
	return domain.Attachment{FileName: name, ContentType: "text/plain; charset=utf-8",
		Size: int64(len(data)), Data: data, UploaderUserID: &uploader, CreatedAt: at}
}

func TestMigration0025AttachmentsShape(t *testing.T) {
	s := newTestDB(t)
	want := map[string]struct {
		typ     string
		notNull int
	}{
		"id": {"INTEGER", 0}, "ticket_id": {"INTEGER", 1}, "comment_id": {"INTEGER", 0},
		"file_name": {"TEXT", 1}, "content_type": {"TEXT", 1}, "size": {"INTEGER", 1},
		"data": {"BLOB", 1}, "uploader_user_id": {"INTEGER", 0}, "created_at": {"TEXT", 1},
	}
	rows, err := s.db.Query(`SELECT name, type, "notnull" FROM pragma_table_info('attachments')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := 0
	for rows.Next() {
		var name, typ string
		var notNull int
		if err := rows.Scan(&name, &typ, &notNull); err != nil {
			t.Fatal(err)
		}
		w, ok := want[name]
		if !ok || w.typ != typ || w.notNull != notNull {
			t.Errorf("column %s %s notnull=%d, want %+v", name, typ, notNull, w)
		}
		got++
	}
	if got != len(want) {
		t.Errorf("columns = %d, want %d", got, len(want))
	}
	fks := map[string]string{}
	fkRows, err := s.db.Query(`SELECT "from", "table", on_delete FROM pragma_foreign_key_list('attachments')`)
	if err != nil {
		t.Fatal(err)
	}
	defer fkRows.Close()
	for fkRows.Next() {
		var from, table, onDelete string
		if err := fkRows.Scan(&from, &table, &onDelete); err != nil {
			t.Fatal(err)
		}
		fks[from] = table + " " + onDelete
	}
	for col, want := range map[string]string{"ticket_id": "tickets CASCADE", "comment_id": "comments CASCADE", "uploader_user_id": "users NO ACTION"} {
		if fks[col] != want {
			t.Errorf("fk %s = %q, want %q", col, fks[col], want)
		}
	}
	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=25`).Scan(&applied); err != nil || applied != 1 {
		t.Errorf("schema_migrations version=25 rows = %d (%v), want 1", applied, err)
	}
}

func TestMigration0025ConstraintsRefuseInconsistentRows(t *testing.T) {
	s := newTestDB(t)
	_, agent, tk := mutationFixture(t, s)
	other := seedTicketForTimelineInCategory(t, s, 2, tk.CategoryID)
	res, err := s.db.Exec(`INSERT INTO comments (ticket_id, author, body, created_at) VALUES (?, 'Ana', 'x', ?)`, other, formatTime(testClock))
	if err != nil {
		t.Fatal(err)
	}
	otherComment, _ := res.LastInsertId()
	insert := func(commentID any, name string, size int, data []byte) error {
		_, err := s.db.Exec(`INSERT INTO attachments (ticket_id, comment_id, file_name, content_type, size, data, uploader_user_id, created_at)
			VALUES (?, ?, ?, 'text/plain', ?, ?, ?, ?)`, tk.ID, commentID, name, size, data, agent, formatTime(testClock))
		return err
	}
	if err := insert(nil, "ok.txt", 2, []byte("ok")); err != nil {
		t.Fatalf("valid row refused: %v", err)
	}
	for name, err := range map[string]error{
		"empty name":                insert(nil, "", 2, []byte("ok")),
		"size does not match data":  insert(nil, "a.txt", 3, []byte("ok")),
		"empty file":                insert(nil, "a.txt", 0, []byte{}),
		"over 10 MB":                insert(nil, "a.txt", domain.MaxAttachmentBytes+1, make([]byte, domain.MaxAttachmentBytes+1)),
		"comment of another ticket": insert(otherComment, "a.txt", 2, []byte("ok")),
	} {
		if err == nil {
			t.Errorf("%s: accepted, want a constraint failure", name)
		}
	}
}

func TestCommentUnitOfWorkStoresAttachmentsWithTheComment(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	_, agent, tk := mutationFixture(t, s)
	at := testClock.Add(time.Minute)
	c := commentMutation(tk.ID, agent, domain.CommentPublic, at)
	files := []domain.Attachment{attachmentFixture("a.txt", []byte("first"), agent, at), attachmentFixture("b.txt", []byte("second"), agent, at)}
	if err := s.CommentUnitOfWork().Add(ctx, application.CommentMutation{Comment: c, Attachments: files}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	list, err := s.AttachmentStore().ListByTicket(ctx, tk.ID, false)
	if err != nil {
		t.Fatalf("ListByTicket: %v", err)
	}
	if len(list) != 2 || list[0].FileName != "a.txt" || list[1].FileName != "b.txt" {
		t.Fatalf("list = %+v, want a.txt then b.txt", list)
	}
	for _, a := range list {
		if a.ID == 0 || a.TicketID != tk.ID || a.CommentID == nil || *a.CommentID != c.ID || a.Data != nil || a.Size == 0 ||
			a.UploaderUserID == nil || *a.UploaderUserID != agent || !a.CreatedAt.Equal(at) {
			t.Errorf("listed attachment = %+v, want metadata of comment %d without data", a, c.ID)
		}
	}
	got, err := s.AttachmentStore().Get(ctx, list[1].ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got.Data, []byte("second")) || got.Visibility != domain.CommentPublic || got.ContentType != "text/plain; charset=utf-8" {
		t.Errorf("Get = %+v, want the second file with public visibility", got)
	}
}

func TestAttachmentStoreHidesInternalCommentFiles(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	_, agent, tk := mutationFixture(t, s)
	public := commentMutation(tk.ID, agent, domain.CommentPublic, testClock)
	internal := commentMutation(tk.ID, agent, domain.CommentInternal, testClock)
	for _, c := range []*domain.Comment{public, internal} {
		if err := s.CommentUnitOfWork().Add(ctx, application.CommentMutation{Comment: c,
			Attachments: []domain.Attachment{attachmentFixture(string(c.Visibility)+".txt", []byte("x"), agent, testClock)}}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	staff, _ := s.AttachmentStore().ListByTicket(ctx, tk.ID, true)
	requester, _ := s.AttachmentStore().ListByTicket(ctx, tk.ID, false)
	if len(staff) != 2 || len(requester) != 1 || requester[0].FileName != "public.txt" {
		t.Errorf("staff=%d requester=%+v, want 2 and only public.txt", len(staff), requester)
	}
	got, err := s.AttachmentStore().Get(ctx, staff[1].ID)
	if err != nil || got.Visibility != domain.CommentInternal {
		t.Errorf("Get internal = %+v, %v, want internal visibility", got, err)
	}
	if _, err := s.AttachmentStore().Get(ctx, 9999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get missing = %v, want ErrNotFound", err)
	}
}

func TestCommentUnitOfWorkRollsBackWhenAnAttachmentFails(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	requester, agent, tk := mutationFixture(t, s)
	if _, err := s.db.Exec(`CREATE TRIGGER trg_test_fail_attachment BEFORE INSERT ON attachments
		WHEN NEW.file_name = 'boom.txt' BEGIN SELECT RAISE(ABORT, 'injected attachment failure'); END`); err != nil {
		t.Fatal(err)
	}
	c := commentMutation(tk.ID, agent, domain.CommentPublic, testClock)
	err := s.CommentUnitOfWork().Add(ctx, application.CommentMutation{Comment: c,
		Notifications: []domain.Notification{{UserID: requester, TicketID: tk.ID, Kind: domain.NotificationCommentPublic, ActorUserID: &agent, CreatedAt: testClock}},
		Attachments:   []domain.Attachment{attachmentFixture("ok.txt", []byte("x"), agent, testClock), attachmentFixture("boom.txt", []byte("x"), agent, testClock)}})
	if err == nil {
		t.Fatal("Add succeeded despite the injected failure")
	}
	for table, want := range map[string]int{"comments": 0, "attachments": 0, "notifications": 0} {
		if n := countRows(t, s, `SELECT COUNT(*) FROM `+table+` WHERE ticket_id=?`, tk.ID); n != want {
			t.Errorf("%s = %d, want %d after rollback", table, n, want)
		}
	}
}

func TestTicketUnitOfWorkCreateStoresCreationAttachments(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	requester := seedUserRole(t, s, "Rita", "rita@x", true, domain.RoleUser)
	cat := seedCategory(t, s, "Att-Cat")
	tk := &domain.Ticket{Title: "with file", CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew,
		RequesterUserID: &requester, CreatedAt: testClock, UpdatedAt: testClock}
	if err := s.TicketUnitOfWork().Create(ctx, application.TicketMutation{Ticket: tk,
		Events:      []domain.AuditEvent{{Actor: "Rita", Action: domain.ActionCreated, CreatedAt: testClock}},
		Attachments: []domain.Attachment{attachmentFixture("captura.txt", []byte("screen"), requester, testClock)}}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	list, err := s.AttachmentStore().ListByTicket(ctx, tk.ID, false)
	if err != nil || len(list) != 1 || list[0].CommentID != nil || list[0].TicketID != tk.ID {
		t.Fatalf("list = %+v, %v, want one creation file", list, err)
	}
	got, _ := s.AttachmentStore().Get(ctx, list[0].ID)
	if got.Visibility != "" || !bytes.Equal(got.Data, []byte("screen")) {
		t.Errorf("Get = %+v, want the creation file with no comment visibility", got)
	}
}

func TestCreateTicketWithRunStoresAttachmentsAtomically(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	def := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do"}}}
	vid := seedPublished(t, s, cat, def)

	in := buildCreateInput(cat, vid, req, def, nil, 0, "active", domain.StateNew, nil)
	in.Attachments = []domain.Attachment{attachmentFixture("log.txt", []byte("trace"), req, testClock)}
	tk, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if list, _ := s.AttachmentStore().ListByTicket(ctx, tk.ID, false); len(list) != 1 || list[0].FileName != "log.txt" {
		t.Fatalf("list = %+v, want log.txt", list)
	}

	// A failing attachment insert rolls the whole creation back.
	if _, err := s.db.Exec(`CREATE TRIGGER trg_test_fail_create_attachment BEFORE INSERT ON attachments
		BEGIN SELECT RAISE(ABORT, 'injected attachment failure'); END`); err != nil {
		t.Fatal(err)
	}
	before := countRows(t, s, `SELECT COUNT(*) FROM tickets`)
	in2 := buildCreateInput(cat, vid, req, def, nil, 0, "active", domain.StateNew, nil)
	in2.Attachments = in.Attachments
	if _, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(ctx, in2); err == nil {
		t.Fatal("create succeeded despite the injected failure")
	}
	if after := countRows(t, s, `SELECT COUNT(*) FROM tickets`); after != before {
		t.Errorf("tickets = %d, want %d (rolled back)", after, before)
	}
}

func TestAttachmentsCascadeWithTheirTicketAndComment(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	_, agent, tk := mutationFixture(t, s)
	c := commentMutation(tk.ID, agent, domain.CommentPublic, testClock)
	if err := s.CommentUnitOfWork().Add(ctx, application.CommentMutation{Comment: c,
		Attachments: []domain.Attachment{attachmentFixture("a.txt", []byte("x"), agent, testClock)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM comments WHERE id=?`, c.ID); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM attachments WHERE ticket_id=?`, tk.ID); n != 0 {
		t.Errorf("attachments after deleting the comment = %d, want 0", n)
	}
}

// TestMigration0025UpgradePathKeepsExistingTickets upgrades a pre-0025
// database holding a ticket and a comment: both survive untouched, the table
// starts empty, and version 25 is recorded.
func TestMigration0025UpgradePathKeepsExistingTickets(t *testing.T) {
	ctx := context.Background()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	pre0025 := fstest.MapFS{}
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Compare(e.Name(), "0025_ticket_attachments.sql") >= 0 {
			continue
		}
		blob, err := fs.ReadFile(migrationsFS, "migrations/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		pre0025["migrations/"+e.Name()] = &fstest.MapFile{Data: blob}
	}
	if err := migrate(ctx, pre.db, pre0025); err != nil {
		t.Fatalf("migrate pre-0025: %v", err)
	}
	if columnExists(t, pre, "attachments", "id") {
		t.Fatal("attachments existed before 0025")
	}
	tk := seedTicketForTimeline(t, pre, 1)
	if _, err := pre.db.Exec(`INSERT INTO comments (ticket_id, author, body, created_at) VALUES (?, 'Ana', 'before', ?)`, tk, formatTime(testClock)); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, pre.db, migrationsFS); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if n := countRows(t, pre, `SELECT COUNT(*) FROM comments WHERE ticket_id=? AND body='before'`, tk); n != 1 {
		t.Errorf("comments after upgrade = %d, want 1", n)
	}
	if n := countRows(t, pre, `SELECT COUNT(*) FROM attachments`); n != 0 {
		t.Errorf("attachments after upgrade = %d, want 0", n)
	}
	if n := countRows(t, pre, `SELECT COUNT(*) FROM schema_migrations WHERE version=25`); n != 1 {
		t.Errorf("version 25 rows = %d, want 1", n)
	}
}
