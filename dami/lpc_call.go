package dami

import (
	"context"
	"strconv"
)

// Call1 invokes a provider method (registered via RegisterProvider) and returns
// its single typed result. topic is "mapping.Method"; args are positional and
// encoded by the provider's Coder. This is the no-codegen consumer path
// (code-gen stubs arrive in P2 as the default). Mirrors the effect of Java's
// lpc.createConsumer for a single call. R must match the provider method's
// return type (a runtime convention).
func Call1[R any](b *Bus, ctx context.Context, topic string, args ...any) (R, error) {
	fut := CallOn[map[string]any, R](b, topic, encodeArgs(args))
	return fut.Get(ctx)
}

// Call0 invokes a provider method that returns only an error (no result).
func Call0(b *Bus, ctx context.Context, topic string, args ...any) error {
	fut := CallOn[map[string]any, any](b, topic, encodeArgs(args))
	_, err := fut.Get(ctx)
	return err
}

func encodeArgs(args []any) map[string]any {
	m := make(map[string]any, len(args))
	for i, a := range args {
		m[strconv.Itoa(i)] = a
	}
	return m
}
