package domain

import (
	"errors"
	"strings"
	"time"
	"unicode"
)

// Folder groups categories in the catalog (issue #484). Folders nest without a
// depth limit: ParentID is nil for a top-level folder. A folder's name is unique
// among its siblings, case-insensitively, and a folder can never become its own
// ancestor. Folders only organize what requesters browse: they own no workflow,
// no desk and no ticket.
type Folder struct {
	ID          int64
	ParentID    *int64
	Name        string
	Description string
	CreatedAt   time.Time
}

// ErrFolderCycle names the store-contract failure where a move would place a
// folder inside itself or one of its own subfolders.
var ErrFolderCycle = errors.New("folder cycle")

// English wording of the folder refusals.
const (
	ErrMsgFolderCycle        = "a folder can't move into itself or one of its subfolders"
	ErrMsgFolderNameRequired = "folder name is required"
)

// FolderCycleError reports a move refused because ParentID is FolderID itself or
// one of its descendants. Nothing was written. It satisfies errors.Is against
// ErrFolderCycle.
type FolderCycleError struct {
	FolderID int64
	ParentID int64
}

func (e *FolderCycleError) Error() string { return ErrMsgFolderCycle }

func (e *FolderCycleError) Is(target error) bool { return target == ErrFolderCycle }

// FolderNameKey is the form two sibling folder names are compared in: case
// folded across Unicode, so "ÁREA" and "área", or "ΟΣ" and "ος", are the same
// name. SQLite's NOCASE folds ASCII only, which is why the key is computed here
// and stored. Each rune becomes the smallest rune of its simple case-folding
// orbit, which makes two keys equal exactly when strings.EqualFold says the
// names are equal (simple folding: "ß" is not "SS").
func FolderNameKey(name string) string {
	return strings.Map(func(r rune) rune {
		lowest := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < lowest {
				lowest = f
			}
		}
		return lowest
	}, name)
}
