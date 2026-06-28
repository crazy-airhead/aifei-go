package dami

import (
	"fmt"
	"reflect"
	"sync"
)

var errorType = reflect.TypeOf((*error)(nil)).Elem()

// Lpc wraps a Bus to offer local-procedure-call (rpc-like, but in-process)
// dispatch over the call pipeline. Providers are registered by reflection; the
// no-codegen consumer path uses the Call0/Call1 helpers (code-gen stubs arrive
// in P2 as the default). Mirrors Java's DamiLpc.
type Lpc struct {
	bus   *Bus
	coder Coder

	mu        sync.Mutex
	providers map[reflect.Type][]topicListen
}

type topicListen struct {
	topic    string
	unlisten func()
}

// NewLpc builds an Lpc over b with the default CoderForIndex.
func NewLpc(b *Bus) *Lpc {
	return &Lpc{bus: b, coder: NewCoderForIndex(), providers: map[reflect.Type][]topicListen{}}
}

// Bus returns the underlying bus.
func (l *Lpc) Bus() *Bus { return l.bus }

// Coder returns the argument coder.
func (l *Lpc) Coder() Coder { return l.coder }

// SetCoder sets the argument coder (default CoderForIndex).
func (l *Lpc) SetCoder(c Coder) {
	if c != nil {
		l.coder = c
	}
}

// RegisterProvider reflects provider's exported methods and registers each as a
// call listener at topic "topicMapping.MethodName". A provider type may be
// registered only once. Mirrors Java DamiLpcImpl.registerProvider; the pattern
// (reflect a struct's methods onto routes) is the same one aifei-go's
// server.Register uses.
//
// Each method becomes a Call handler: arguments are decoded from the request
// payload by the Coder (positionally, since Go reflection can't read parameter
// names), the method is invoked by reflection, and its first result plus any
// trailing error becomes the call reply. Methods follow the Go convention
// func(args...) (R, error) or func(args...) error.
func (l *Lpc) RegisterProvider(topicMapping string, provider any) error {
	v := reflect.ValueOf(provider)
	t := v.Type()
	if t.Kind() != reflect.Pointer || v.IsNil() {
		return fmt.Errorf("dami: provider must be a non-nil pointer, got %T", provider)
	}

	l.mu.Lock()
	if _, exists := l.providers[t]; exists {
		l.mu.Unlock()
		return fmt.Errorf("dami: provider %s already registered", t)
	}
	l.providers[t] = nil // reserve the slot against concurrent registration
	l.mu.Unlock()

	var records []topicListen
	for i := 0; i < t.NumMethod(); i++ {
		m := t.Method(i)
		if m.PkgPath != "" {
			continue // unexported
		}
		topic := topicMapping + "." + m.Name
		unlisten := l.registerMethod(topic, v, m)
		records = append(records, topicListen{topic: topic, unlisten: unlisten})
	}

	l.mu.Lock()
	l.providers[t] = records
	l.mu.Unlock()
	return nil
}

// UnregisterProvider removes every listener registered for provider's type.
func (l *Lpc) UnregisterProvider(provider any) {
	t := reflect.TypeOf(provider)
	l.mu.Lock()
	records := l.providers[t]
	delete(l.providers, t)
	l.mu.Unlock()
	for _, r := range records {
		if r.unlisten != nil {
			r.unlisten()
		}
	}
}

func (l *Lpc) registerMethod(topic string, v reflect.Value, m reflect.Method) func() {
	handler := func(data map[string]any) (any, error) {
		args, err := l.coder.Decode(m, data)
		if err != nil {
			return nil, err
		}
		return l.invoke(v, m, args)
	}
	return ListenCallOn(l.bus, topic, handler)
}

// invoke calls m on v with the coder-decoded positional args and returns the
// first result plus any trailing error. nil args fall back to the zero value of
// the method's declared parameter type.
func (l *Lpc) invoke(v reflect.Value, m reflect.Method, args []any) (any, error) {
	in := make([]reflect.Value, 0, len(args)+1)
	in = append(in, v)
	for i, a := range args {
		if a == nil {
			in = append(in, reflect.Zero(m.Type.In(i+1))) // In(0) is the receiver
			continue
		}
		in = append(in, reflect.ValueOf(a))
	}
	out := m.Func.Call(in)
	n := len(out)
	resultCount := n
	var err error
	if n > 0 && out[n-1].Type() == errorType {
		if !out[n-1].IsNil() {
			err = out[n-1].Interface().(error)
		}
		resultCount = n - 1
	}
	if resultCount == 0 {
		return nil, err
	}
	return out[0].Interface(), err
}
