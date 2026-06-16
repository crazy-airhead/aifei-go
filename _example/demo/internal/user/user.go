package user

type User struct {
	*BaseUser
}

// New creates a new User ready for use with GetBean and Insert.
func New() *User {
	return &User{BaseUser: NewBase()}
}
