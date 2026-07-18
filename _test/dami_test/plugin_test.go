package dami_test

import (
	"testing"

	adami "github.com/crazy-airhead/aifei-go/dami"
	pdami "github.com/crazy-airhead/aifei-go/plugins/dami"
)

func TestPluginLifecycle(t *testing.T) {
	p, err := pdami.NewPlugin(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}

	// The plugin's bus is now the core package default; a listener registered via
	// the core helpers hits it.
	var got string
	adami.Listen("test.plugin.lifecycle", func(e *adami.Event[string]) error {
		got = e.Payload
		return nil
	})
	if _, err := adami.Send("test.plugin.lifecycle", "hi"); err != nil {
		t.Fatal(err)
	}
	if got != "hi" {
		t.Fatalf("got=%q want hi", got)
	}
	if p.Bus() == nil || p.Lpc() == nil {
		t.Fatal("Bus and Lpc must be exposed")
	}

	// Stop clears every listener.
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := adami.Send("test.plugin.lifecycle", "again"); err != nil {
		t.Fatal(err)
	}
	if got == "again" {
		t.Fatal("listener should have been removed by Stop")
	}

	// Reset the default bus so other tests sharing the package default stay
	// isolated.
	adami.SetDefaultBus(adami.New())
}

func TestPluginCustomRouter(t *testing.T) {
	// Options flow through to the owned bus.
	p, err := pdami.NewPlugin(nil, adami.WithRouter(adami.NewPathRouter()))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Bus().Router().(*adami.PathRouter); !ok {
		t.Fatal("custom router option not applied")
	}
}
