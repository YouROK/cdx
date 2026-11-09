package session

import "context"

// TurnInfo describes the turn that owns the current context.
type TurnInfo struct {
	SessionID string
	RootID    string
	Depth     int
	Workdir   string
	Preset    string
	Model     string
}

type turnInfoKey struct{}

// WithTurn attaches the running turn to a context.
func WithTurn(ctx context.Context, info TurnInfo) context.Context {
	return context.WithValue(ctx, turnInfoKey{}, info)
}

// TurnFromContext returns the turn attached to ctx.
func TurnFromContext(ctx context.Context) (TurnInfo, bool) {
	if ctx == nil {
		return TurnInfo{}, false
	}
	info, ok := ctx.Value(turnInfoKey{}).(TurnInfo)
	return info, ok
}
