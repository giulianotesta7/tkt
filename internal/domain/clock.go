package domain

import "time"

// Clock is the injectable time source (D7). The domain never calls
// time.Now() directly; callers pass the current instant into transitions
// and field updates.
type Clock interface {
	Now() time.Time
}
