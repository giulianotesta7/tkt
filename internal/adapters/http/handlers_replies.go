package httpadapter

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// SavedReplyHandlers implement the saved-reply manager (issue #418): one screen
// holding the actor's own replies and, for every desk they may see there, that
// desk's shared replies. It is the screen the composer's picker links to, and
// the only place a reply's text is edited.
//
// One route per operation, so a route only ever writes the one thing it names
// (the shape the preferences routes already follow):
//
//	GET  /replies                                     the screen; ?desk=<id> selects a
//	                                                  desk tab, ?new=1 and ?edit=<id>
//	                                                  open the editor
//	POST /replies                                     create a personal reply
//	POST /replies/{id}/edit                           edit one personal reply
//	POST /replies/{id}/delete                         delete one personal reply
//	POST /replies/desks/{deskID}/replies              create a reply shared with that desk
//	POST /replies/desks/{deskID}/replies/{id}/edit    edit one desk reply
//	POST /replies/desks/{deskID}/replies/{id}/delete  delete one desk reply
//
// A desk reply's routes name the desk as well as the reply, exactly like the
// desk membership route names its desk: the screen then knows which tab a
// successful or refused submission belongs to without trusting a form field,
// and it can refuse a reply the named desk does not hold. The desk routes are
// longer than the personal ones on purpose — a three-segment
// /replies/desks/{deskID} would overlap /replies/{id}/edit on a request such as
// /replies/desks/edit, neither pattern would be more specific, and the mux
// refuses to register that pair.
//
// Every route requires the internal-comment capability, which is what a role
// that works inside a ticket holds: the screen is a staff tool, and a requester
// is refused before anything is read or written. The use cases then decide what
// each staff member may change (a personal reply is its owner's, a desk's
// replies belong to desk management) and answer 404 for anything they will not
// admit exists.
//
// The screen is a plain document: every mutation is the form's own native POST,
// answered by a 303 back to the tab it was submitted from with the shared save
// feedback, and a refusal re-renders the screen with the message. No route here
// needs HTMX, so nothing on this screen depends on a script to be usable.
type SavedReplyHandlers struct {
	replies  *application.CannedReplyService
	renderer *Renderer
}

// NewSavedReplyHandlers wires the saved-reply screen against the saved-reply
// use cases and the renderer.
func NewSavedReplyHandlers(replies *application.CannedReplyService, renderer *Renderer) *SavedReplyHandlers {
	return &SavedReplyHandlers{replies: replies, renderer: renderer}
}

// Register mounts the saved-reply routes.
func (h *SavedReplyHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /replies", h.index)
	mux.HandleFunc("POST /replies", h.createPersonal)
	mux.HandleFunc("POST /replies/{id}/edit", h.updatePersonal)
	mux.HandleFunc("POST /replies/{id}/delete", h.deletePersonal)
	mux.HandleFunc("POST /replies/desks/{deskID}/replies", h.createForDesk)
	mux.HandleFunc("POST /replies/desks/{deskID}/replies/{id}/edit", h.updateForDesk)
	mux.HandleFunc("POST /replies/desks/{deskID}/replies/{id}/delete", h.deleteForDesk)
}

// savedReplyTab is one tab of the strip: the actor's own replies, or one desk's
// shared replies. Editable says whether THIS actor may change them, which is
// the difference between the admin's view of a desk and the read-only view a
// member of that desk gets.
type savedReplyTab struct {
	Key      string
	Label    string
	Href     string
	Active   bool
	DeskID   int64
	Editable bool
	Help     string
	Replies  []domain.CannedReply
}

// savedReplyTabMine is the key of the always-present first tab.
const savedReplyTabMine = "mine"

// EditHref opens this tab's editor on one of its replies. It is a query on the
// tab, because the editor lives on the screen rather than on a page of its own.
func (t savedReplyTab) EditHref(id int64) string {
	return editedTabHref(t.Href, "edit="+strconv.FormatInt(id, 10))
}

// DeleteHref is the route that deletes one of this tab's replies.
func (t savedReplyTab) DeleteHref(id int64) string {
	if t.DeskID == 0 {
		return "/replies/" + strconv.FormatInt(id, 10) + "/delete"
	}
	return "/replies/desks/" + strconv.FormatInt(t.DeskID, 10) + "/replies/" + strconv.FormatInt(id, 10) + "/delete"
}

// savedReplyEditor is the editor's server-side state. It opens from the URL
// (a new reply, or one reply to edit) and reopens with the submitted text when
// a submission is refused, so a rejected title never costs the typed reply.
// ReplyID 0 means a creation; DeskID 0 means a personal reply.
type savedReplyEditor struct {
	Open    bool
	ReplyID int64
	DeskID  int64
	Title   string
	Body    string
}

// Action is the route the open editor posts to: the collection for a creation,
// the reply's own edit route otherwise.
func (e savedReplyEditor) Action() string {
	switch {
	case e.ReplyID == 0 && e.DeskID == 0:
		return "/replies"
	case e.ReplyID == 0:
		return "/replies/desks/" + strconv.FormatInt(e.DeskID, 10) + "/replies"
	case e.DeskID == 0:
		return "/replies/" + strconv.FormatInt(e.ReplyID, 10) + "/edit"
	default:
		return "/replies/desks/" + strconv.FormatInt(e.DeskID, 10) + "/replies/" + strconv.FormatInt(e.ReplyID, 10) + "/edit"
	}
}

// savedRepliesData is the screen's payload. Tabs carries the strip and Active
// the one it shows, so the template renders the tab contract once.
type savedRepliesData struct {
	pageData
	Error      string
	Tabs       []savedReplyTab
	Active     savedReplyTab
	Editor     savedReplyEditor
	NewHref    string
	CancelHref string
}

// index renders the screen for the session actor. Nothing is mutated here: the
// tab, and whether the editor is open, come from the query the tab links and
// the reply buttons generate.
func (h *SavedReplyHandlers) index(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapCommentInternal) {
		return
	}
	view, err := h.replies.Manager(r.Context(), *userFromContext(r.Context()))
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	tabs := savedReplyTabs(i18n.New(requestLocale(r)), view)
	tab, found := savedReplyTabByDesk(tabs, parseID(r.URL.Query().Get("desk")))
	if !found {
		// The desk exists, but not on this actor's screen: it answers the same
		// not-found a reply of a desk they are not a member of does, because
		// the tab list is the only place these replies were offered.
		httpError(w, r, "error.desk_not_found", http.StatusNotFound)
		return
	}
	editor, err := editorFromQuery(r, tab)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	h.renderer.Render(w, r, "replies", "", savedRepliesPageData(r, tabs, tab, editor, ""), http.StatusOK)
}

// createPersonal stores a reply owned by the actor.
func (h *SavedReplyHandlers) createPersonal(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	editor := savedReplyEditor{Open: true, Title: r.Form.Get("title"), Body: r.Form.Get("body")}
	if _, err := h.replies.CreatePersonal(r.Context(), *userFromContext(r.Context()), editor.Title, editor.Body); err != nil {
		h.refusal(w, r, 0, editor, err)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, "/replies")
}

// updatePersonal replaces the text of one of the actor's own replies.
func (h *SavedReplyHandlers) updatePersonal(w http.ResponseWriter, r *http.Request) {
	id, ok := savedReplyID(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	editor := savedReplyEditor{Open: true, ReplyID: id, Title: r.Form.Get("title"), Body: r.Form.Get("body")}
	if _, err := h.replies.UpdatePersonal(r.Context(), *userFromContext(r.Context()), id, editor.Title, editor.Body); err != nil {
		h.refusal(w, r, 0, editor, err)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, savedReplyTabHref(0))
}

// deletePersonal removes one of the actor's own replies.
func (h *SavedReplyHandlers) deletePersonal(w http.ResponseWriter, r *http.Request) {
	id, ok := savedReplyID(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if err := h.replies.DeletePersonal(r.Context(), *userFromContext(r.Context()), id); err != nil {
		writeMappedError(w, r, err)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, savedReplyTabHref(0))
}

// createForDesk stores a reply shared with one desk. Only desk management may
// do it; a member of the desk reads the result but never writes it.
func (h *SavedReplyHandlers) createForDesk(w http.ResponseWriter, r *http.Request) {
	deskID, _, ok := h.deskGroup(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	editor := savedReplyEditor{Open: true, DeskID: deskID, Title: r.Form.Get("title"), Body: r.Form.Get("body")}
	if _, err := h.replies.CreateForDesk(r.Context(), *userFromContext(r.Context()), deskID, editor.Title, editor.Body); err != nil {
		h.refusal(w, r, deskID, editor, err)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, savedReplyTabHref(deskID))
}

// updateForDesk replaces the text of one desk's shared reply.
func (h *SavedReplyHandlers) updateForDesk(w http.ResponseWriter, r *http.Request) {
	deskID, id, ok := h.deskReplyTarget(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	editor := savedReplyEditor{Open: true, ReplyID: id, DeskID: deskID, Title: r.Form.Get("title"), Body: r.Form.Get("body")}
	if _, err := h.replies.UpdateForDesk(r.Context(), *userFromContext(r.Context()), id, editor.Title, editor.Body); err != nil {
		h.refusal(w, r, deskID, editor, err)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, savedReplyTabHref(deskID))
}

// deleteForDesk removes one desk's shared reply.
func (h *SavedReplyHandlers) deleteForDesk(w http.ResponseWriter, r *http.Request) {
	deskID, id, ok := h.deskReplyTarget(w, r)
	if !ok {
		return
	}
	if err := h.replies.DeleteForDesk(r.Context(), *userFromContext(r.Context()), id); err != nil {
		writeMappedError(w, r, err)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, savedReplyTabHref(deskID))
}

// refusal answers a refused submission. When the actor may read the screen, it
// renders that screen again with the message, and a rejected text reopens the
// editor on what they typed, so a refused title costs nothing. A refusal that is
// not about the text (the reply is not theirs, or desk management is not theirs)
// shows the message on the tab instead, with no editor over a reply they cannot
// change. An actor the read itself refuses (a requester) and a storage failure
// answer the message alone: rendering the manager there would hand them the
// screen.
func (h *SavedReplyHandlers) refusal(w http.ResponseWriter, r *http.Request, deskID int64, editor savedReplyEditor, err error) {
	status, message := mapErrorFor(r, err)
	view, viewErr := h.replies.Manager(r.Context(), *userFromContext(r.Context()))
	if viewErr != nil || status == http.StatusInternalServerError {
		http.Error(w, message, status)
		return
	}
	if status != http.StatusUnprocessableEntity {
		editor = savedReplyEditor{}
	}
	tabs := savedReplyTabs(i18n.New(requestLocale(r)), view)
	h.renderer.Render(w, r, "replies", "", savedRepliesPageData(r, tabs, savedReplyTabFor(tabs, deskID), editor, message), status)
}

// savedReplyID reads the {id} path parameter. A malformed id is a 400 before
// anything is read or written.
func savedReplyID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id := parseID(r.PathValue("id"))
	if id == 0 {
		httpError(w, r, "error.invalid_saved_reply_id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

// deskGroup resolves the desk a desk route names against the actor's own screen.
// The desk must be one this actor's tabs carry, so a route can neither address a
// desk the screen never offered nor leave the actor on a tab they cannot see,
// and a requester is refused here with the screen's staff-only refusal before
// any desk is written.
func (h *SavedReplyHandlers) deskGroup(w http.ResponseWriter, r *http.Request) (int64, application.CannedReplyGroup, bool) {
	deskID := parseID(r.PathValue("deskID"))
	if deskID == 0 {
		httpError(w, r, "error.invalid_desk_id", http.StatusBadRequest)
		return 0, application.CannedReplyGroup{}, false
	}
	view, err := h.replies.Manager(r.Context(), *userFromContext(r.Context()))
	if err != nil {
		writeMappedError(w, r, err)
		return 0, application.CannedReplyGroup{}, false
	}
	for _, group := range view.Desks {
		if group.DeskID == deskID {
			return deskID, group, true
		}
	}
	httpError(w, r, "error.desk_not_found", http.StatusNotFound)
	return 0, application.CannedReplyGroup{}, false
}

// deskReplyTarget resolves a desk-reply mutation's two path values. The reply
// must belong to the desk the URL names, so a URL cannot change a reply through
// a desk it does not belong to; anything else answers not found, exactly as the
// reply of a desk the actor is not a member of does.
func (h *SavedReplyHandlers) deskReplyTarget(w http.ResponseWriter, r *http.Request) (deskID, replyID int64, ok bool) {
	replyID, ok = savedReplyID(w, r)
	if !ok {
		return 0, 0, false
	}
	deskID, group, ok := h.deskGroup(w, r)
	if !ok {
		return 0, 0, false
	}
	for _, reply := range group.Replies {
		if reply.ID == replyID {
			return deskID, replyID, true
		}
	}
	writeMappedError(w, r, &domain.NotFoundError{Kind: "canned reply", ID: replyID})
	return 0, 0, false
}

// writeMappedError answers an error through the shared status table and the
// instance's copy, which is what keeps a 403 and a 404 readable in both
// languages.
func writeMappedError(w http.ResponseWriter, r *http.Request, err error) {
	status, message := mapErrorFor(r, err)
	http.Error(w, message, status)
}

// savedRepliesPageData builds the screen's payload for one request.
func savedRepliesPageData(r *http.Request, tabs []savedReplyTab, tab savedReplyTab, editor savedReplyEditor, message string) savedRepliesData {
	return newSavedRepliesData(pageDataFrom(r, "replies"), tabs, tab, editor, message)
}

// newSavedRepliesData builds the screen's payload from the shell data the
// caller already assembled, the tabs, the tab to show, the editor's state and
// an optional message. It is the pure half, so a fixture can freeze the screen
// without a request.
func newSavedRepliesData(page pageData, tabs []savedReplyTab, tab savedReplyTab, editor savedReplyEditor, message string) savedRepliesData {
	page.SavedRepliesAssets = true
	data := savedRepliesData{pageData: page, Error: message, Editor: editor, Tabs: tabs}
	for i := range data.Tabs {
		if data.Tabs[i].Key != tab.Key {
			continue
		}
		data.Tabs[i].Active = true
		data.Active = data.Tabs[i]
	}
	// An editor that opened from a query always posts back to the tab it came
	// from, and the two links are relative to that tab, so cancelling and
	// creating stay in place instead of dropping the actor on the first tab.
	data.NewHref = editedTabHref(data.Active.Href, "new=1")
	data.CancelHref = data.Active.Href
	return data
}

// savedReplyTabs builds the strip: the actor's own replies first, then one tab
// per desk the service returned, which is already narrowed to the desks this
// actor may see on this screen and ordered by desk name.
func savedReplyTabs(tr i18n.Translator, view *application.CannedReplyManagerView) []savedReplyTab {
	tabs := []savedReplyTab{{
		Key:      savedReplyTabMine,
		Label:    tr.T("saved_replies.tab.mine"),
		Href:     "/replies",
		Editable: true,
		Help:     tr.T("saved_replies.help.mine"),
		Replies:  view.Mine,
	}}
	for _, group := range view.Desks {
		help := tr.Tf("saved_replies.help.desk_readonly", group.DeskName)
		if group.Editable {
			help = tr.Tf("saved_replies.help.desk_editable", group.DeskName)
		}
		tabs = append(tabs, savedReplyTab{
			Key:      strconv.FormatInt(group.DeskID, 10),
			Label:    group.DeskName,
			Href:     savedReplyTabHref(group.DeskID),
			DeskID:   group.DeskID,
			Editable: group.Editable,
			Help:     help,
			Replies:  group.Replies,
		})
	}
	return tabs
}

// savedReplyTabByDesk resolves one tab by desk id (0 is the actor's own). The
// second result is false for a desk this actor's view does not carry.
func savedReplyTabByDesk(tabs []savedReplyTab, deskID int64) (savedReplyTab, bool) {
	for _, tab := range tabs {
		if tab.DeskID == deskID {
			return tab, true
		}
	}
	return savedReplyTab{}, false
}

// savedReplyTabFor is the lenient form the refusal path uses: a desk the view
// does not carry falls back to the actor's own tab, which every screen has.
func savedReplyTabFor(tabs []savedReplyTab, deskID int64) savedReplyTab {
	if tab, ok := savedReplyTabByDesk(tabs, deskID); ok {
		return tab
	}
	return tabs[0]
}

// savedReplyTabHref is one tab's URL.
func savedReplyTabHref(deskID int64) string {
	if deskID == 0 {
		return "/replies"
	}
	return "/replies?desk=" + strconv.FormatInt(deskID, 10)
}

// editedTabHref appends one editor parameter to a tab's URL.
func editedTabHref(href, parameter string) string {
	if strings.Contains(href, "?") {
		return href + "&" + parameter
	}
	return href + "?" + parameter
}

// editorFromQuery opens the editor from the tab it belongs to: ?new=1 starts a
// creation, ?edit=<id> starts an edit of a reply the tab actually carries, and
// an id the tab does not list answers not found instead of rendering an editor
// over nothing.
func editorFromQuery(r *http.Request, tab savedReplyTab) (savedReplyEditor, error) {
	query := r.URL.Query()
	if query.Get("new") == "1" {
		return savedReplyEditor{Open: true, DeskID: tab.DeskID}, nil
	}
	id := parseID(query.Get("edit"))
	if id == 0 {
		return savedReplyEditor{}, nil
	}
	for _, reply := range tab.Replies {
		if reply.ID == id {
			return savedReplyEditor{Open: true, ReplyID: reply.ID, DeskID: tab.DeskID, Title: reply.Title, Body: reply.Body}, nil
		}
	}
	return savedReplyEditor{}, &domain.NotFoundError{Kind: "canned reply", ID: id}
}
