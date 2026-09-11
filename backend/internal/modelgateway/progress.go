package modelgateway

import "context"

type Progress struct {
	Done          bool
	Reasoning     string
	Tool          string
	ArgumentBytes int
	Arguments     string
}
type progressKey struct{}

func WithProgress(ctx context.Context, fn func(Progress)) context.Context {
	return context.WithValue(ctx, progressKey{}, fn)
}
func reportProgress(ctx context.Context, p Progress) {
	if fn, ok := ctx.Value(progressKey{}).(func(Progress)); ok {
		fn(p)
	}
}
