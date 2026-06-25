package json

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/crazy-airhead/aifei-go/nami"
)

// Decoder is the JSON decoder for Nami RPC calls.
// It deserializes response bodies using Go's standard encoding/json.
type Decoder struct{}

// NewDecoder creates a new JSON Decoder.
func NewDecoder() *Decoder {
	return &Decoder{}
}

// Enctype returns "application/json".
func (d *Decoder) Enctype() string {
	return nami.JSONValue
}

// Decode deserializes the result body into the target type.
func (d *Decoder) Decode(rst *nami.Result, typ reflect.Type) (any, error) {
	if len(rst.Body()) == 0 {
		return nil, nil
	}

	str := rst.BodyAsString()
	if str == "null" || str == "" {
		return nil, nil
	}

	// If target type is string and the response doesn't look like JSON, return raw
	if typ.Kind() == reflect.String && len(str) > 0 &&
		str[0] != '"' && str[0] != '{' && str[0] != '[' {
		return str, nil
	}

	// Handle pointer types
	isPtr := typ.Kind() == reflect.Pointer
	elemType := typ
	if isPtr {
		elemType = typ.Elem()
	}

	val := reflect.New(elemType).Interface()

	if err := json.Unmarshal([]byte(str), val); err != nil {
		// If unmarshal fails and target is string, return raw
		if typ.Kind() == reflect.String {
			return str, nil
		}
		return nil, fmt.Errorf("nami json decode: type %s: %w", typ.String(), err)
	}

	if isPtr {
		return val, nil
	}
	return reflect.ValueOf(val).Elem().Interface(), nil
}

// Pretreatment sets the Accept header to application/json.
func (d *Decoder) Pretreatment(ctx *nami.Context) {
	ctx.Headers[nami.HeaderAccept] = nami.JSONValue
}
