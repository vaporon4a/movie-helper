package content

import "context"

type preparationAttemptKey struct{}

// Attempts are zero-based and persisted by the scheduler. Later attempts read
// other Wikipedia titles instead of repeatedly asking AI about the same three.
func WithPreparationAttempt(ctx context.Context, attempt int) context.Context {
	return context.WithValue(ctx, preparationAttemptKey{}, attempt)
}

func preparationAttempt(ctx context.Context) int {
	n, _ := ctx.Value(preparationAttemptKey{}).(int)
	return max(n, 0)
}
