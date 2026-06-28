// Package dami is an in-process event bus for decoupling modules within a single
// process — a Go port of noear's DamiBus (dami2 2.0.5).
//
// It routes events by topic to typed listeners, supporting broadcasting,
// interceptors, ordered listeners, attachment passing, fallbacks and pluggable
// routers (hash / path / tag) and dispatchers — the "bus" half of DamiBus.
//
// P0 implements event broadcasting (Send/Listen), the three routers, the
// interceptor chain, listener ordering, attachments, the handled flag and
// fallbacks. P1 adds request-response (Call), streaming (Stream) and local
// procedure call (Lpc), all building on the same pipeline via RequestPayload +
// Sink. The codegen tool (tools/damigen, Enjoy templates) and the aifei.Plugin
// adapter (plugins/dami) live in separate modules; this package itself stays
// zero-dependency.
//
// Quick start:
//
//	b := dami.New()
//	un := b.Listen("user.created", func(e *dami.Event[User]) error {
//	    log.Print(e.Payload) // Payload is typed User
//	    return nil
//	})
//	defer un()
//	ev, _ := b.Send("user.created", user)
//	fmt.Println(ev.Handled()) // true
//
// A package-level default bus mirrors Java's Dami.bus():
//
//	dami.Listen("user.created", func(e *dami.Event[User]) error { ... })
//	dami.Send("user.created", user)
//
// See docs/dami/02-migration-design.md for the full design.
package dami
