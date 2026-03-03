package domain

import "errors"

const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// User 聚合根
type User struct {
	id           string
	email        string
	passwordHash string
	status       string // "active" 或 "banned"
	role         string // "user" or "admin"
}

// 恢复工厂
func ReconstituteUser(id, email, hash, status string) *User {
	return &User{id: id, email: email, passwordHash: hash, status: status, role: RoleUser}
}

// ReconstituteUserWithRole restores a User with an explicit role.
func ReconstituteUserWithRole(id, email, hash, status, role string) *User {
	if role == "" {
		role = RoleUser
	}
	return &User{id: id, email: email, passwordHash: hash, status: status, role: role}
}

// 核心业务规则：验证登录逻辑
func (u *User) Authenticate(plainPassword string, compareFn func(plain, hash string) bool) error {
	if u.status != "active" {
		return errors.New("domain_error: 账号已被封禁或未激活")
	}
	if !compareFn(plainPassword, u.passwordHash) {
		return errors.New("domain_error: 密码错误")
	}
	return nil
}

func (u *User) IsAdmin() bool { return u.role == RoleAdmin }

// 暴露只读数据
func (u *User) ID() string           { return u.id }
func (u *User) Email() string        { return u.email }
func (u *User) PasswordHash() string { return u.passwordHash }
func (u *User) Status() string       { return u.status }
func (u *User) Role() string         { return u.role }
