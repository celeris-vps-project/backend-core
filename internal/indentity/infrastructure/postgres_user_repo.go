package infrastructure

import (
	"backend-core/internal/indentity/domain"
	"errors"

	"gorm.io/gorm"
)

// UserPO 是專門給 GORM 用的持久化對象 (完全隔離了底層與業務)
type UserPO struct {
	ID           string `gorm:"primaryKey;column:id"`
	Email        string `gorm:"uniqueIndex;column:email"`
	PasswordHash string `gorm:"column:password_hash"`
	Status       string `gorm:"column:status"`
}

// TableName 指定資料庫表名
func (UserPO) TableName() string {
	return "users"
}

type PostgresUserRepo struct {
	db *gorm.DB
}

func (r *PostgresUserRepo) Save(u *domain.User) error {
	if u == nil {
		return errors.New("user is nil")
	}

	po := UserPO{
		ID:           u.ID(),
		Email:        u.Email(),
		PasswordHash: u.PasswordHash(),
		Status:       u.Status(),
	}

	return r.db.Create(&po).Error
}

// NewPostgresUserRepo 實例化 Repo
func NewPostgresUserRepo(db *gorm.DB) *PostgresUserRepo {
	return &PostgresUserRepo{db: db}
}

// FindByEmail 實現了 domain.UserRepository 介面
func (r *PostgresUserRepo) FindByEmail(email string) (*domain.User, error) {
	var po UserPO

	// 使用 GORM 查詢資料庫
	err := r.db.Where("email = ?", email).First(&po).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("user not found") // 轉換為業務層理解的錯誤
		}
		return nil, err
	}

	// 【關鍵點】將資料庫模型 (UserPO) 恢復為領域層的聚合根 (domain.User)
	return domain.ReconstituteUser(po.ID, po.Email, po.PasswordHash, po.Status), nil
}
