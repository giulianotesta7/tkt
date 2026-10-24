package application

import (
	"context"
	"sort"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// PendingApproval is one account an administrator approves, with the role it
// receives. Approval grants an ordinary role only: root is never an approvable
// role, so this path neither creates nor promotes one (issue #419, S9).
type PendingApproval struct {
	ID   int64
	Role domain.Role
}

// PendingUsersService implements the Users > Pending tab (issue #419, S3): the
// accounts a company sign-in created that wait for an administrator's approval,
// and the bulk approval that turns a chosen set of them into ordinary managed
// users.
//
// The service owns the decisions the store must not make: who may see the list,
// who may approve, and which role may be granted. It reuses the managed-user
// rules — the capability gate, the closed role set, the root refusal, and the
// grant matrix — so approval can never hand out a privilege that an ordinary
// edit could not (S9).
type PendingUsersService struct {
	pending PendingUserStore
	clock   domain.Clock
}

// NewPendingUsersService wires the Pending tab use cases against the given
// ports.
func NewPendingUsersService(pending PendingUserStore, clock domain.Clock) *PendingUsersService {
	return &PendingUsersService{pending: pending, clock: clock}
}

// PendingAccount is one account waiting for approval together with the sign-in
// methods its stored identities carry (issue #419, S3/S12). The providers are
// the closed provider set, in display order; an account with no stored identity
// carries none, and the presentation layer says so instead of guessing one.
type PendingAccount struct {
	User      domain.User
	Providers []CompanyProvider
}

// List answers the accounts waiting for approval to an authorized management
// actor. It carries no other account: a pending one is inactive, so nothing
// else lists it either.
func (s *PendingUsersService) List(ctx context.Context, actor domain.User) ([]domain.User, error) {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageUsers) {
		return nil, domain.NewForbiddenError("user management is not permitted")
	}
	return s.pending.ListPending(ctx)
}

// ListWithSignIn answers the same accounts as List, each with the sign-in
// methods its stored identities carry. It reads the identities as facts: the
// account list decides which accounts answer, and a stored identity the closed
// provider set does not name is still reported (the presentation layer labels it
// explicitly) rather than dropped or replaced by a guess.
func (s *PendingUsersService) ListWithSignIn(ctx context.Context, actor domain.User) ([]PendingAccount, error) {
	users, err := s.List(ctx, actor)
	if err != nil {
		return nil, err
	}
	signIns, err := s.pending.ListPendingSignIn(ctx)
	if err != nil {
		return nil, err
	}
	return mergePendingSignIn(users, signIns), nil
}

// mergePendingSignIn pairs each waiting account with its own provider rows, in
// the providers' display order, so the answer is deterministic whatever order
// the store used. An account with no row keeps an empty list.
func mergePendingSignIn(users []domain.User, signIns []PendingSignIn) []PendingAccount {
	byUser := make(map[int64][]CompanyProvider, len(users))
	for _, signIn := range signIns {
		byUser[signIn.UserID] = append(byUser[signIn.UserID], signIn.Provider)
	}
	accounts := make([]PendingAccount, 0, len(users))
	for _, user := range users {
		providers := byUser[user.ID]
		sort.SliceStable(providers, func(i, j int) bool {
			return providerDisplayIndex(providers[i]) < providerDisplayIndex(providers[j])
		})
		accounts = append(accounts, PendingAccount{User: user, Providers: providers})
	}
	return accounts
}

// providerDisplayIndex answers where a provider sits in the closed provider set;
// a value the set does not name sorts after every named one, so it stays visible
// at the end instead of being reordered into a claim.
func providerDisplayIndex(provider CompanyProvider) int {
	for index, known := range CompanyProviders {
		if provider == known {
			return index
		}
	}
	return len(CompanyProviders)
}

// Approve turns the selected accounts into managed users: each one becomes
// active, stops being pending, and receives the chosen role, in ONE transaction
// that either applies every entry or writes nothing.
//
// The checks run in the managed-user order the edit path already uses: the
// capability gate first, then the batch shape, then the role of every entry
// (closed set, never root, grantable by this actor). A refusal therefore never
// reaches the store, and a stale entry that the store rejects rolls the whole
// batch back — a bulk approval cannot leave half a decision behind.
func (s *PendingUsersService) Approve(ctx context.Context, actor domain.User, approvals []PendingApproval) ([]domain.User, error) {
	policy := NewPolicy()
	if !policy.Capabilities(actor.Role).Require(CapManageUsers) {
		return nil, domain.NewForbiddenError("user management is not permitted")
	}
	if err := ValidatePendingApprovals(approvals); err != nil {
		return nil, err
	}
	for _, approval := range approvals {
		if approval.Role == domain.RoleRoot {
			return nil, domain.NewRootProtectedError()
		}
		if !policy.CanGrantUserRole(actor.Role, approval.Role) {
			return nil, domain.NewForbiddenError("admin accounts require root")
		}
	}
	return s.pending.ApprovePending(ctx, approvals, actor.ID, s.clock.Now())
}

// ValidatePendingApprovals checks the batch shape both the service and the
// store refuse: at least one account, no id repeated, every role a real one.
// The store calls it as its own backstop, so a direct caller cannot bypass the
// rules the use case applies. Batch-shape refusals carry catalogue keys for the Pending tab form.
func ValidatePendingApprovals(approvals []PendingApproval) error {
	if len(approvals) == 0 {
		return &domain.ValidationError{Field: "users", Message: "select at least one account to approve", Key: "error.pending_approval_selection_required"}
	}
	seen := make(map[int64]bool, len(approvals))
	for _, approval := range approvals {
		if !approval.Role.Valid() {
			return &domain.ValidationError{Field: "role", Message: "invalid role", Key: "error.invalid_role"}
		}
		if seen[approval.ID] {
			return &domain.ValidationError{Field: "users", Message: "an account is listed twice", Key: "error.pending_approval_duplicate_account"}
		}
		seen[approval.ID] = true
	}
	return nil
}
