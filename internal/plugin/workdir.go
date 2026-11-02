package plugin

import "context"

type workdirContextKey struct{}

// WithWorkdir attaches the active session working directory to a call context.
func WithWorkdir(ctx context.Context, workdir string) context.Context {
	if workdir == "" {
		return ctx
	}
	return context.WithValue(ctx, workdirContextKey{}, workdir)
}

// WorkdirFromContext returns the session working directory attached to ctx, or an empty string.
func WorkdirFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	workdir, _ := ctx.Value(workdirContextKey{}).(string)
	return workdir
}
