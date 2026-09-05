package modelgateway

import (
	"context"
	"time"
)

// CallObservation contains accounting only, never prompts or provider errors.
type CallObservation struct {
	Phase      string        `json:"phase"`
	Model      string        `json:"model"`
	DurationMS int64         `json:"duration_ms"`
	Usage      *Usage        `json:"usage"`
	Failed     bool          `json:"failed"`
	Budget     *BudgetReport `json:"budget,omitempty"`
}
type observerKey struct{}
type phaseKey struct{}

func WithObserver(ctx context.Context, observer func(CallObservation)) context.Context {
	return context.WithValue(ctx, observerKey{}, observer)
}
func WithPhase(ctx context.Context, phase string) context.Context {
	return context.WithValue(ctx, phaseKey{}, phase)
}
func (c *Client) observe(ctx context.Context, options Options, started time.Time, usage *Usage, budget *BudgetReport, err error) {
	if observer, ok := ctx.Value(observerKey{}).(func(CallObservation)); ok {
		phase, _ := ctx.Value(phaseKey{}).(string)
		if phase == "" {
			phase = "model"
		}
		observer(CallObservation{Phase: phase, Model: c.ResolveModel(options), DurationMS: time.Since(started).Milliseconds(), Usage: usage, Budget: budget, Failed: err != nil})
	}
}
