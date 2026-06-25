package nami

import "sync"

// NamiManager is the global registry for channels, encoders, decoders, and filters.
// All registration methods are safe for concurrent use.
var (
	mu           sync.RWMutex
	decoderMap   = make(map[string]Decoder)
	encoderMap   = make(map[string]Encoder)
	channelMap   = make(map[string]Channel)
	filterSet    []Filter
	decoderFirst Decoder
	encoderFirst Encoder
)

// RegDecoder registers a Decoder by its enctype. The first registered becomes the default.
func RegDecoder(d Decoder) {
	mu.Lock()
	defer mu.Unlock()
	decoderMap[d.Enctype()] = d
	if decoderFirst == nil {
		decoderFirst = d
	}
}

// RegDecoderIfAbsent registers a Decoder only if its enctype is not already registered.
func RegDecoderIfAbsent(d Decoder) {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := decoderMap[d.Enctype()]; !ok {
		decoderMap[d.Enctype()] = d
	}
	if decoderFirst == nil {
		decoderFirst = d
	}
}

// RegEncoder registers an Encoder by its enctype. The first registered becomes the default.
func RegEncoder(e Encoder) {
	mu.Lock()
	defer mu.Unlock()
	encoderMap[e.Enctype()] = e
	if encoderFirst == nil {
		encoderFirst = e
	}
}

// RegEncoderIfAbsent registers an Encoder only if its enctype is not already registered.
func RegEncoderIfAbsent(e Encoder) {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := encoderMap[e.Enctype()]; !ok {
		encoderMap[e.Enctype()] = e
	}
	if encoderFirst == nil {
		encoderFirst = e
	}
}

// RegChannel registers a Channel by scheme (e.g. "http", "https").
func RegChannel(scheme string, ch Channel) {
	mu.Lock()
	defer mu.Unlock()
	channelMap[scheme] = ch
}

// RegChannelIfAbsent registers a Channel only if its scheme is not already registered.
func RegChannelIfAbsent(scheme string, ch Channel) {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := channelMap[scheme]; !ok {
		channelMap[scheme] = ch
	}
}

// RegFilter registers a global Filter.
func RegFilter(f Filter) {
	mu.Lock()
	defer mu.Unlock()
	filterSet = append(filterSet, f)
}

// GetDecoder returns the Decoder for the given enctype.
func GetDecoder(enctype string) Decoder {
	mu.RLock()
	defer mu.RUnlock()
	return decoderMap[enctype]
}

// GetDecoderFirst returns the first registered Decoder.
func GetDecoderFirst() Decoder {
	mu.RLock()
	defer mu.RUnlock()
	return decoderFirst
}

// GetEncoder returns the Encoder for the given enctype.
func GetEncoder(enctype string) Encoder {
	mu.RLock()
	defer mu.RUnlock()
	return encoderMap[enctype]
}

// GetEncoderFirst returns the first registered Encoder.
func GetEncoderFirst() Encoder {
	mu.RLock()
	defer mu.RUnlock()
	return encoderFirst
}

// GetChannel returns the Channel for the given scheme.
func GetChannel(scheme string) Channel {
	mu.RLock()
	defer mu.RUnlock()
	return channelMap[scheme]
}

// GetFilters returns a copy of the global filter list.
func GetFilters() []Filter {
	mu.RLock()
	defer mu.RUnlock()
	result := make([]Filter, len(filterSet))
	copy(result, filterSet)
	return result
}
