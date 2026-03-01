package infra

import (
	"backend-core/internal/identity/domain"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type JWTService struct {
	secret []byte
	issuer string
}

func NewJWTService(secret string, issuer string) *JWTService {
	return &JWTService{
		secret: []byte(secret),
		issuer: issuer,
	}
}

// Generate 實現了 app.TokenGenerator 介面
func (s *JWTService) Generate(user *domain.User) (string, error) {
	// 構造 JWT 的 Payload (Claims)
	claims := jwt.RegisteredClaims{
		Subject:   user.ID(), // 把 UserID 存入 sub 欄位
		Issuer:    s.issuer,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)), // 24小時後過期
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}

	// 使用 HS256 算法簽名
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}

// ParseToken 提供給 Hertz 中間件使用的解碼器
func (s *JWTService) ParseToken(tokenString string) (string, error) {
	// 解析 Token 並校驗簽名算法
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.secret, nil
	})

	if err != nil || !token.Valid {
		return "", errors.New("invalid or expired token")
	}

	// 提取 UserID (Subject)
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", errors.New("invalid claims payload")
	}

	sub, err := claims.GetSubject()
	if err != nil {
		return "", err
	}

	return sub, nil // 成功回傳 UserID
}
