// Package json provides JSON Encoder/Decoder implementations for Nami.
package json

import (
	"encoding/json"

	"github.com/crazy-airhead/aifei-go/nami"
)

// Encoder is the JSON encoder for Nami RPC calls.
// It serializes objects using Go's standard encoding/json.
type Encoder struct{}

// NewEncoder creates a new JSON Encoder.
func NewEncoder() *Encoder {
	return &Encoder{}
}

// Enctype returns "application/json".
func (e *Encoder) Enctype() string {
	return nami.JSONValue
}

// BodyRequired returns false — a body is optional for JSON encoding.
func (e *Encoder) BodyRequired() bool {
	return false
}

// Encode serializes obj to JSON bytes.
func (e *Encoder) Encode(obj any) ([]byte, error) {
	return json.Marshal(obj)
}

// Pretreatment is a no-op for JSON encoder.
func (e *Encoder) Pretreatment(ctx *nami.Context) {}

func init() {
	nami.RegEncoder(NewEncoder())
	nami.RegDecoder(NewDecoder())
}
