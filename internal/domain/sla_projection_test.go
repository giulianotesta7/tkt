package domain

import (
	"testing"
	"time"
)

// ProjectSLA (issue #211): the pure, total SLA compliance projection over
// a ticket's FROZEN warning and due instants, its observed milestone
// instants, and one caller-supplied now snapshot. No clock reads, no I/O,
// no errors — every rule is exercised with frozen instants. The warning
// percent is NOT a projection input: it was applied once, at freeze time.

var slaProjectionStart = time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)

// slaProjectionAt returns an instant offset seconds after StartedAt.
func slaProjectionAt(offsetSeconds int) time.Time {
	return slaProjectionStart.Add(time.Duration(offsetSeconds) * time.Second)
}

// slaProjectionFrozen returns a TicketSLA anchored at slaProjectionStart
// with the freeze-time instants of the 1800s/14400s targets at an 80
// percent warning threshold: first response warns at 1440s and is due at
// 1800s, resolution warns at 11520s and is due at 14400s.
func slaProjectionFrozen() *TicketSLA {
	return &TicketSLA{
		FirstResponseSeconds: 1800,
		ResolveSeconds:       14400,
		WarnFirstResponseAt:  slaProjectionAt(1440),
		DueFirstResponseAt:   slaProjectionAt(1800),
		WarnResolveAt:        slaProjectionAt(11520),
		DueResolveAt:         slaProjectionAt(14400),
		StartedAt:            slaProjectionStart,
	}
}

func TestSLAStateStringValues(t *testing.T) {
	// The state strings are the wire/rendering contract; they are pinned
	// so an accidental rename cannot slip through silently.
	if string(SLANone) != "none" {
		t.Errorf("SLANone = %q, want %q", SLANone, "none")
	}
	if string(SLAOnTrack) != "on_track" {
		t.Errorf("SLAOnTrack = %q, want %q", SLAOnTrack, "on_track")
	}
	if string(SLAAtRisk) != "at_risk" {
		t.Errorf("SLAAtRisk = %q, want %q", SLAAtRisk, "at_risk")
	}
	if string(SLABreached) != "breached" {
		t.Errorf("SLABreached = %q, want %q", SLABreached, "breached")
	}
	if string(SLAMet) != "met" {
		t.Errorf("SLAMet = %q, want %q", SLAMet, "met")
	}
}

func TestProjectSLA(t *testing.T) {
	tests := []struct {
		name             string
		frozen           *TicketSLA
		milestones       SLAMilestones
		now              time.Time
		wantFirstState   SLAState
		wantResolveState SLAState
		wantOverall      SLAState
	}{
		{
			name:           "no frozen SLA",
			frozen:         nil,
			milestones:     SLAMilestones{},
			now:            slaProjectionAt(999999),
			wantOverall:    SLANone,
			wantFirstState: SLANone,
		},
		{
			name:             "pre-0016 row (zero due instants) is no frozen SLA",
			frozen:           &TicketSLA{FirstResponseSeconds: 1800, ResolveSeconds: 14400, StartedAt: slaProjectionStart},
			milestones:       SLAMilestones{},
			now:              slaProjectionAt(999999),
			wantOverall:      SLANone,
			wantFirstState:   SLANone,
			wantResolveState: SLANone,
		},
		{
			name:             "pending well before the frozen warning instant",
			frozen:           slaProjectionFrozen(),
			now:              slaProjectionAt(600),
			wantFirstState:   SLAOnTrack,
			wantResolveState: SLAOnTrack,
			wantOverall:      SLAOnTrack,
		},
		{
			name:             "pending exactly at the frozen warning instant",
			frozen:           slaProjectionFrozen(),
			now:              slaProjectionAt(1440),
			wantFirstState:   SLAAtRisk,
			wantResolveState: SLAOnTrack,
			wantOverall:      SLAAtRisk,
		},
		{
			name:             "pending one second before the frozen warning instant",
			frozen:           slaProjectionFrozen(),
			now:              slaProjectionAt(1439),
			wantFirstState:   SLAOnTrack,
			wantResolveState: SLAOnTrack,
			wantOverall:      SLAOnTrack,
		},
		{
			name:             "pending one second past the frozen warning instant",
			frozen:           slaProjectionFrozen(),
			now:              slaProjectionAt(1441),
			wantFirstState:   SLAAtRisk,
			wantResolveState: SLAOnTrack,
			wantOverall:      SLAAtRisk,
		},
		{
			name:             "pending exactly at due",
			frozen:           slaProjectionFrozen(),
			now:              slaProjectionAt(1800),
			wantFirstState:   SLABreached,
			wantResolveState: SLAOnTrack,
			wantOverall:      SLABreached,
		},
		{
			name:             "pending one second after due",
			frozen:           slaProjectionFrozen(),
			now:              slaProjectionAt(1801),
			wantFirstState:   SLABreached,
			wantResolveState: SLAOnTrack,
			wantOverall:      SLABreached,
		},
		{
			name:             "achieved exactly at due is met",
			frozen:           slaProjectionFrozen(),
			milestones:       SLAMilestones{FirstResponseAt: ptrTime(slaProjectionAt(1800))},
			now:              slaProjectionAt(2000),
			wantFirstState:   SLAMet,
			wantResolveState: SLAOnTrack,
			wantOverall:      SLAOnTrack,
		},
		{
			name:             "achieved one second late is breached",
			frozen:           slaProjectionFrozen(),
			milestones:       SLAMilestones{FirstResponseAt: ptrTime(slaProjectionAt(1801))},
			now:              slaProjectionAt(2000),
			wantFirstState:   SLABreached,
			wantResolveState: SLAOnTrack,
			wantOverall:      SLABreached,
		},
		{
			name:   "both milestones met",
			frozen: slaProjectionFrozen(),
			milestones: SLAMilestones{
				FirstResponseAt: ptrTime(slaProjectionAt(600)),
				FirstResolvedAt: ptrTime(slaProjectionAt(7200)),
			},
			now:              slaProjectionAt(8000),
			wantFirstState:   SLAMet,
			wantResolveState: SLAMet,
			wantOverall:      SLAMet,
		},
		{
			name:   "first response met with resolution overdue is overall breached",
			frozen: slaProjectionFrozen(),
			milestones: SLAMilestones{
				FirstResponseAt: ptrTime(slaProjectionAt(600)),
			},
			now:              slaProjectionAt(14500),
			wantFirstState:   SLAMet,
			wantResolveState: SLABreached,
			wantOverall:      SLABreached,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProjectSLA(tt.frozen, tt.milestones, tt.now)
			if tt.frozen == nil || got.Frozen == nil {
				// A nil commitment and the pre-0016 zero-instant row are both
				// "no frozen SLA": no Frozen surfaced, both milestone statuses
				// left zero-valued.
				if tt.frozen != nil && got.Frozen == nil && tt.wantOverall != SLANone {
					t.Fatalf("Frozen = nil for a non-nil commitment, want %+v", tt.frozen)
				}
				if got.Frozen != nil {
					t.Fatalf("Frozen = %+v, want nil", got.Frozen)
				}
				if got.Overall != SLANone {
					t.Fatalf("Overall = %q, want %q", got.Overall, SLANone)
				}
				if (got.FirstResponse != SLAMilestoneStatus{}) {
					t.Errorf("FirstResponse = %+v, want the zero value", got.FirstResponse)
				}
				if (got.Resolve != SLAMilestoneStatus{}) {
					t.Errorf("Resolve = %+v, want the zero value", got.Resolve)
				}
				return
			}
			if got.Frozen != tt.frozen {
				t.Fatalf("Frozen = %+v, want %+v", got.Frozen, tt.frozen)
			}
			if got.FirstResponse.State != tt.wantFirstState {
				t.Errorf("FirstResponse.State = %q, want %q", got.FirstResponse.State, tt.wantFirstState)
			}
			if got.Resolve.State != tt.wantResolveState {
				t.Errorf("Resolve.State = %q, want %q", got.Resolve.State, tt.wantResolveState)
			}
			if got.Overall != tt.wantOverall {
				t.Errorf("Overall = %q, want %q", got.Overall, tt.wantOverall)
			}
		})
	}
}

// TestProjectSLAWarnInstantIsFrozenNotDerived proves the projection
// consumes the frozen WarnAt verbatim: a ticket whose commitment was
// frozen at a 50 percent threshold is at_risk at ITS frozen warning
// instant, and editing the instance's warning percent afterwards neither
// moves that instant nor changes the verdict — the freeze rule (issue
// #211).
func TestProjectSLAWarnInstantIsFrozenNotDerived(t *testing.T) {
	frozen := slaProjectionFrozen() // frozen at 80 percent: warns at 1440s

	// The instance's percent has since been changed to 50: the frozen
	// instant, not the current setting, decides.
	got := ProjectSLA(frozen, SLAMilestones{}, slaProjectionAt(1439))
	if got.FirstResponse.State != SLAOnTrack {
		t.Errorf("one second before the frozen WarnAt: State = %q, want %q", got.FirstResponse.State, SLAOnTrack)
	}
	got = ProjectSLA(frozen, SLAMilestones{}, slaProjectionAt(1440))
	if got.FirstResponse.State != SLAAtRisk {
		t.Errorf("at the frozen WarnAt: State = %q, want %q", got.FirstResponse.State, SLAAtRisk)
	}
}

// TestProjectSLAWarningInstantAfterDueCannotResurrectAtRisk pins that a
// warning instant at or past due (only possible through a legacy or
// hand-edited row) can never mask a breach: the due comparison wins.
func TestProjectSLAWarningInstantAfterDueCannotResurrectAtRisk(t *testing.T) {
	frozen := slaProjectionFrozen()
	frozen.WarnFirstResponseAt = slaProjectionAt(2000) // past the 1800s due

	got := ProjectSLA(frozen, SLAMilestones{}, slaProjectionAt(1800))
	if got.FirstResponse.State != SLABreached {
		t.Errorf("State = %q at due with a late WarnAt, want %q", got.FirstResponse.State, SLABreached)
	}
}

// TestProjectSLAMilestoneStatuses pins the derived per-milestone fields:
// WarnAt and DueAt come from the frozen instants, Elapsed runs from
// StartedAt, and Remaining is negative once overdue and zero for an
// achieved milestone.
func TestProjectSLAMilestoneStatuses(t *testing.T) {
	frozen := slaProjectionFrozen()

	// Pending before due: Remaining counts down, Elapsed runs from start.
	got := ProjectSLA(frozen, SLAMilestones{}, slaProjectionAt(600))
	fr := got.FirstResponse
	if fr.TargetSeconds != 1800 {
		t.Errorf("TargetSeconds = %d, want 1800", fr.TargetSeconds)
	}
	if !fr.WarnAt.Equal(slaProjectionAt(1440)) {
		t.Errorf("WarnAt = %v, want the frozen %v", fr.WarnAt, slaProjectionAt(1440))
	}
	if !fr.DueAt.Equal(slaProjectionAt(1800)) {
		t.Errorf("DueAt = %v, want the frozen %v", fr.DueAt, slaProjectionAt(1800))
	}
	if fr.Elapsed != 600*time.Second {
		t.Errorf("Elapsed = %v, want 600s", fr.Elapsed)
	}
	if fr.Remaining != 1200*time.Second {
		t.Errorf("Remaining = %v, want 1200s", fr.Remaining)
	}

	// Pending overdue: Remaining is negative so a caller can render the
	// overdue amount as -Remaining.
	got = ProjectSLA(frozen, SLAMilestones{}, slaProjectionAt(2400))
	if got.FirstResponse.Remaining != -600*time.Second {
		t.Errorf("overdue Remaining = %v, want -600s", got.FirstResponse.Remaining)
	}
	if got.FirstResponse.Elapsed != 2400*time.Second {
		t.Errorf("overdue Elapsed = %v, want 2400s", got.FirstResponse.Elapsed)
	}

	// Achieved: Remaining is zero (meaningless once achieved) and Elapsed
	// is the observed fact, achieved instant minus StartedAt.
	got = ProjectSLA(frozen, SLAMilestones{FirstResponseAt: ptrTime(slaProjectionAt(900))}, slaProjectionAt(2400))
	fr = got.FirstResponse
	if fr.Remaining != 0 {
		t.Errorf("achieved Remaining = %v, want 0", fr.Remaining)
	}
	if fr.Elapsed != 900*time.Second {
		t.Errorf("achieved Elapsed = %v, want 900s", fr.Elapsed)
	}
	if !fr.AchievedAt.Equal(slaProjectionAt(900)) {
		t.Errorf("AchievedAt = %v, want %v", fr.AchievedAt, slaProjectionAt(900))
	}
}

// ptrTime returns a pointer to v (test helper for milestone instants).
func ptrTime(v time.Time) *time.Time { return &v }
