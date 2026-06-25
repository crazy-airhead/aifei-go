package nami

// Content type constants.
const (
	JSONValue           = "application/json"
	FormURLEncodedValue = "application/x-www-form-urlencoded"
	FormDataValue       = "multipart/form-data"
	HessianValue        = "application/hessian"
	FuryValue           = "application/fury"
	KryoValue           = "application/kryo"
	ProtobufValue       = "application/protobuf"
	ABCValue            = "application/abc"
)

// HTTP header constants.
const (
	HeaderSerialization = "X-Serialization"
	HeaderContentType   = "Content-Type"
	HeaderAccept        = "Accept"
)

// HTTP method constants.
const (
	MethodGet    = "GET"
	MethodPost   = "POST"
	MethodPut    = "PUT"
	MethodDelete = "DELETE"
)
