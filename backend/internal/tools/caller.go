package tools

import "context"

// Who is asking.
//
// Almost every tool is indifferent to this: an API answers the same way
// whoever called it. A built-in that describes the person asking is the
// exception, and it needs to be told rather than allowed to guess - so the
// identity travels on the context, set once where the request is already
// authenticated, and is read only by the built-ins that ask for it.
//
// Deliberately a copy of a few fields rather than a handle to the database: a
// tool must not be able to widen its own view of the person calling it.
type Caller struct {
	UserID      string
	UserName    string
	UserEmail   string
	UserRole    string
	WorkspaceID string
	// The workspace's own name, and this person's role in it - "owner",
	// "admin" or "member". Empty when the call is not framed by a workspace.
	WorkspaceName string
	WorkspaceRole string
}

type callerKey struct{}

// WithCaller records who a run of tools is on behalf of.
func WithCaller(ctx context.Context, caller Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, caller)
}

// CallerFrom reads it back. The second result is false where nobody set one,
// which a built-in reports as a refusal rather than inventing an answer.
func CallerFrom(ctx context.Context) (Caller, bool) {
	caller, ok := ctx.Value(callerKey{}).(Caller)
	return caller, ok && caller.UserID != ""
}
