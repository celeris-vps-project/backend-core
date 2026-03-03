package infra

import (
	"backend-core/internal/identity/domain"
	"errors"

	"gorm.io/gorm"
)

// SqliteUserRepo 使用 SQLite 作為儲存介面
// UserPO 結構在 postgres_user_repo.go 中定義，這裡直接復用
// 以保持資料模型一致
type SqliteUserRepo struct {
	db *gorm.DB
}

func (r *SqliteUserRepo) Save(u *domain.User) error {
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

// NewSqliteUserRepo 實例化 Repo
func NewSqliteUserRepo(db *gorm.DB) *SqliteUserRepo {
	return &SqliteUserRepo{db: db}
}

// FindByEmail 實現了 domain.UserRepository 介面
func (r *SqliteUserRepo) FindByEmail(email string) (*domain.User, error) {
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
