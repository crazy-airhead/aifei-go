package dami

import (
	"fmt"
	"reflect"
	"strconv"
)

// Coder converts between a provider method's arguments and an event payload, for
// the Lpc layer (P1). Mirrors Java's Coder. Go reflection cannot read parameter
// names, so the default CoderForIndex aligns arguments by position (Java's
// CoderForIndex equivalent); name alignment is supplied by code-generated stubs.
//
// It is defined in P0 so the Event/payload shape is settled before Lpc arrives.
type Coder interface {
	// Encode turns a method's args into a payload.
	Encode(method reflect.Method, args []any) any
	// Decode turns a payload back into method args.
	Decode(method reflect.Method, payload any) ([]any, error)
}

// CoderForIndex encodes args as {"0": a0, "1": a1, ...} and decodes by position.
type CoderForIndex struct{}

// NewCoderForIndex returns the position-aligned coder.
func NewCoderForIndex() Coder { return CoderForIndex{} }

// Encode implements Coder.
func (CoderForIndex) Encode(_ reflect.Method, args []any) any {
	m := make(map[string]any, len(args))
	for i, a := range args {
		m[strconv.Itoa(i)] = a
	}
	return m
}

// Decode implements Coder. method.Type.In(0) is the receiver; the remaining ins
// are the real arguments, decoded positionally.
func (CoderForIndex) Decode(method reflect.Method, payload any) ([]any, error) {
	m, ok := payload.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("dami: coder expects map[string]any payload, got %T", payload)
	}
	n := method.Type.NumIn() - 1
	out := make([]any, n)
	for i := 0; i < n; i++ {
		out[i] = m[strconv.Itoa(i)]
	}
	return out, nil
}
