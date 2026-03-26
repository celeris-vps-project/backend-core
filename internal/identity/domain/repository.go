package domain

import "context"

// UserRepository 接口
type UserRepository interface {
	FindByEmail(ctx context.Context, email string) (*User, error)
	Save(ctx context.Context, u *User) error
}
