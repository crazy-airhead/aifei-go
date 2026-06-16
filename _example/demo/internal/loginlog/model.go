package loginlog

type LoginLog struct {
	*BaseLoginLog
}

// New creates a new LoginLog ready for use with GetBean and Insert.
func New() *LoginLog {
	return &LoginLog{BaseLoginLog: NewBase()}
}
