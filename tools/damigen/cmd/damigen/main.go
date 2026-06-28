// Command dami-gen generates strongly-typed consumer stubs for dami providers.
//
// Scan a package for interfaces annotated with //dami:provider <topic> and emit
// a dami_gen.go with XxxClient implementations that delegate to dami.Call1/Call0.
//
// Usage:
//
//	dami-gen -src ./mysvc -out ./mysvc
//
// Typical invocation via go:generate:
//
//	//go:generate go run github.com/crazy-airhead/aifei-go/tools/damigen/cmd/damigen -src . -out .
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/crazy-airhead/aifei-go/tools/damigen"
)

func main() {
	src := flag.String("src", ".", "source directory containing //dami:provider interfaces")
	out := flag.String("out", ".", "output directory for the generated dami_gen.go")
	flag.Parse()

	if err := damigen.Generate(*src, *out); err != nil {
		fmt.Fprintf(os.Stderr, "dami-gen: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "dami-gen: generated %s\n", *out)
}
