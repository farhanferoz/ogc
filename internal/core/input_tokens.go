package core

import "context"

type inputTokensKey struct{}

// WithInputTokens carries the proxy's own count of the request's input tokens
// down to the stream writer. A translated upstream reports usage only when the
// turn ends, but Claude Code reads the context percentage from message_start,
// so without this the status line sits at 0% and then jumps.
func WithInputTokens(ctx context.Context, tokens int) context.Context {
	if tokens <= 0 {
		return ctx
	}
	return context.WithValue(ctx, inputTokensKey{}, tokens)
}

// InputTokensFromContext returns the count carried by WithInputTokens, or 0.
func InputTokensFromContext(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	tokens, _ := ctx.Value(inputTokensKey{}).(int)
	return tokens
}
