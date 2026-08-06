package domain

import "time"

// Session is a server-side login session (D14). ID is an opaque random
// 32-byte token (hex-encoded). GetByID treats a past ExpiresAt as not found.
type Session struct {
	ID        string
	UserID    int64
	ExpiresAt time.Time
}
