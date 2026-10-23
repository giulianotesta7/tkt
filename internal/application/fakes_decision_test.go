package application

import (
	"context"
	"time"
)

// fakeDecisionModel records the call it receives and answers what the test
// arranged. It records the deadline the suggester set, so the two-second bound
// is proven without a sleep (go-tests rule 9).
type fakeDecisionModel struct {
	answer      DecisionChoiceAnswer
	err         error
	calls       int
	gotConfig   DecisionModelConfig
	gotRequest  DecisionChoiceRequest
	gotDeadline time.Time
	hasDeadline bool
}

func (f *fakeDecisionModel) Choose(ctx context.Context, cfg DecisionModelConfig, req DecisionChoiceRequest) (DecisionChoiceAnswer, error) {
	f.calls++
	f.gotConfig = cfg
	f.gotRequest = req
	f.gotDeadline, f.hasDeadline = ctx.Deadline()
	if f.err != nil {
		return DecisionChoiceAnswer{}, f.err
	}
	return f.answer, nil
}
