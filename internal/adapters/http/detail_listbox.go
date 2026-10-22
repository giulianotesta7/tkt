package httpadapter

import (
	"strconv"

	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// The ticket detail's property pickers (issue #386, S5). The maintainer chose
// the listbox menu (design/ux-redesign/detail-edit-options.html, option A) over
// an autosaving select: a closed native select is not passive — with focus, the
// arrow keys and (in some browsers) the wheel change the option and fire
// `change`, which is exactly how a ticket was reassigned by accident before
// issue #232. So the current value is a BUTTON, the options are real submit
// buttons of the existing form, and with the popover closed nothing reacts to
// the keyboard or the wheel.
//
// The payload is built here instead of in handlers_tickets.go for the same
// reason the SLA panel is: every detailData — including the golden fixtures —
// then renders the identical component from its own values.

// listboxView is one property picker: the button that shows the current value
// and the popover holding the form's option buttons.
type listboxView struct {
	// ID is the opener's id (#ticket-priority, #assign-user): the stable handle
	// the client script and the browser journeys resolve.
	ID string
	// Name is the field the form submits (priority, user_id). Every option
	// button carries it, so a pick is one native field of one form.
	Name string
	// Kind selects the mark the button and the options paint: "priority" (a
	// dot in the level's colour) or "person" (a circle with initials, because a
	// circle means a person in this system).
	Kind string
	// Label is the accessible name of the listbox.
	Label string
	// Filter is the search field's placeholder, empty when the popover does not
	// filter (the priority list is four fixed values).
	Filter string
	// InactiveLabel marks an option whose account is deactivated: the person
	// stays visible on a ticket they still hold, labelled instead of hidden.
	InactiveLabel string
	// Current is the value shown on the button, Level its priority level and
	// Initials the person mark it paints.
	Current  string
	Level    string
	Initials string
	Options  []listboxOption
}

// listboxOption is one option button: a submit button of the surrounding form,
// so the pick never depends on scripted form submission.
type listboxOption struct {
	Value    string
	Label    string
	Level    string
	Initials string
	Selected bool
	Inactive bool
}

// newPriorityPicker builds the Priority picker: one option per priority in the
// domain's own order, the stored one marked selected.
func newPriorityPicker(tr i18n.Translator, current domain.Priority) listboxView {
	options := make([]listboxOption, 0, len(listPriorities))
	for _, priority := range listPriorities {
		options = append(options, listboxOption{
			Value:    string(priority),
			Label:    labeledValue(tr, "priority", priority),
			Level:    string(priority),
			Selected: priority == current,
		})
	}
	return listboxView{
		ID:      "ticket-priority",
		Name:    "priority",
		Kind:    "priority",
		Label:   tr.T("tickets.detail.priority"),
		Current: labeledValue(tr, "priority", current),
		Level:   string(current),
		Options: options,
	}
}

// newAssigneePicker builds the Assignee picker from the same candidate list the
// old dropdown used: the active people plus the already-assigned inactive one
// (the handler appends it), who stays visible and labelled instead of
// disappearing from a ticket they still hold.
func newAssigneePicker(tr i18n.Translator, current string, users []domain.User) listboxView {
	unassigned := tr.T("tickets.detail.unassigned")
	view := listboxView{
		ID:            "assign-user",
		Name:          "user_id",
		Kind:          "person",
		Label:         tr.T("tickets.detail.assignee"),
		Filter:        tr.T("tickets.detail.filter_assignees"),
		InactiveLabel: tr.T("tickets.detail.inactive"),
		Current:       unassigned,
		Options:       make([]listboxOption, 0, len(users)+1),
	}
	view.Options = append(view.Options, listboxOption{
		Value:    "",
		Label:    unassigned,
		Selected: current == "",
	})
	for _, user := range users {
		value := strconv.FormatInt(user.ID, 10)
		selected := value == current
		if selected {
			view.Current = user.Name
			view.Initials = initialsOf(user.Name)
		}
		view.Options = append(view.Options, listboxOption{
			Value:    value,
			Label:    user.Name,
			Initials: initialsOf(user.Name),
			Selected: selected,
			Inactive: !user.Active,
		})
	}
	return view
}
