package infrastructure

import (
	"golang.org/x/crypto/bcrypt"
)

// BcryptPasswordService 实现了 application 层的接口契约
type BcryptPasswordService struct {
	cost int // 计算成本，数值越大越安全，但消耗的 CPU 时间也越长
}

// NewBcryptPasswordService 实例化密码服务
// 如果不确定，可以传入 bcrypt.DefaultCost (通常是 10)
func NewBcryptPasswordService(cost int) *BcryptPasswordService {
	if cost < bcrypt.MinCost {
		cost = bcrypt.DefaultCost
	}
	return &BcryptPasswordService{cost: cost}
}

// Compare 实现密码比对（供登录用例使用）
// 注意：bcrypt 的 Compare 方法接收的顺序是 (hash, plain)
func (s *BcryptPasswordService) Compare(plain, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
	// 如果 err 为 nil，说明密码完全正确
	return err == nil
}

// Hash 生成密码哈希（供注册或修改密码用例使用）
func (s *BcryptPasswordService) Hash(plain string) (string, error) {
	// GenerateFromPassword 会自动生成随机盐并与密码结合进行哈希
	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(plain), s.cost)
	if err != nil {
		return "", err
	}
	return string(hashedBytes), nil
}
