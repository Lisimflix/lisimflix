package contracts

import "context"

// Principal is the authenticated caller the runtime hands to other modules.
// It is internal: the json:"-" tags keep it out of any public response.
type Principal struct {
	AccountID     string `json:"-"`
	ProfileID     string `json:"-"`
	AuthSessionID string `json:"-"`
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom reports false when no authenticated caller is attached.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
