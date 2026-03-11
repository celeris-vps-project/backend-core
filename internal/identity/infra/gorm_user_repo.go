package infra

import (
	"backend-core/internal/identity/domain"
	"errors"

	"gorm.io/gorm"
)

// UserPO 是專門給 GORM 用的持久化對象 (完全隔離了底層與業務)
type UserPO struct {
	ID           string `gorm:"primaryKey;column:id"`
	Email        string `gorm:"uniqueIndex;column:email"`
	PasswordHash string `gorm:"column:password_hash"`
	Status       string `gorm:"column:status"`
	Role         string `gorm:"column:role;default:user"`
}

// TableName 指定資料庫表名
func (UserPO) TableName() string {
	return "users"
}

// GormUserRepo implements domain.UserRepository using GORM.
// It is driver-agnostic: works with SQLite, PostgreSQL, or any GORM-supported database.
type GormUserRepo struct {
	db *gorm.DB
}

// NewGormUserRepo creates a new GormUserRepo.
func NewGormUserRepo(db *gorm.DB) *GormUserRepo {
	return &GormUserRepo{db: db}
}

func (r *GormUserRepo) Save(u *domain.User) error {
	if u == nil {
		return errors.New("user is nil")
	}

	po := UserPO{
		ID:           u.ID(),
		Email:        u.Email(),
		PasswordHash: u.PasswordHash(),
		Status:       u.Status(),
		Role:         u.Role(),
	}

	return r.db.Create(&po).Error
}

// FindByEmail 實現了 domain.UserRepository 介面
func (r *GormUserRepo) FindByEmail(email string) (*domain.User, error) {
	var po UserPO

	err := r.db.Where("email = ?", email).First(&po).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("user not found")
		}
		return nil, err
	}

	return domain.ReconstituteUserWithRole(po.ID, po.Email, po.PasswordHash, po.Status, po.Role), nil
}
