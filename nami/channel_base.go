package nami

// ChannelBase provides the pretreatment logic shared by channel implementations.
// Channels should embed this or call Pretreatment before executing a call.
type ChannelBase struct{}

// Pretreatment initializes decoder and encoder from context headers if not already set.
func (cb *ChannelBase) Pretreatment(ctx *Context) {
	if ctx.Config.Decoder() == nil {
		at := ctx.Headers[HeaderAccept]
		if at == "" {
			at = JSONValue
		}
		ctx.Config.SetDecoder(GetDecoder(at))
		if ctx.Config.Decoder() == nil {
			ctx.Config.SetDecoder(GetDecoderFirst())
		}
	}

	if ctx.Config.Encoder() == nil {
		ct := ctx.Headers[HeaderContentType]
		if ct != "" {
			ctx.Config.SetEncoder(GetEncoder(ct))
		}
	}
}
