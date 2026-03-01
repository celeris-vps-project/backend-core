package domain

// UserRepository 接口
type UserRepository interface {
	FindByEmail(email string) (*User, error)
	Save(u *User) error
}
