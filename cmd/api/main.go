package main

import (
	"backend-core/internal/indentity/application"
	"backend-core/internal/indentity/infrastructure"
	"backend-core/internal/indentity/interfaces/http/middleware"
	"log"

	"github.com/cloudwego/hertz/pkg/app/server"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	identityHttp "backend-core/internal/indentity/interfaces/http"
)

func main() {
	// 1. 連接 PostgreSQL (請替換為你的真實 DSN)
	dsn := "host=localhost user=postgres password=root dbname=whmcs_killer port=5432 sslmode=disable TimeZone=Asia/Taipei"
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("無法連接資料庫: %v", err)
	}

	// (可選) 自動遷移表結構
	db.AutoMigrate(&infrastructure.UserPO{})

	// 2. 實例化真實的基礎設施
	pwdHasher := infrastructure.NewBcryptPasswordService(bcrypt.DefaultCost)
	userRepo := infrastructure.NewPostgresUserRepo(db)
	jwtService := infrastructure.NewJWTService("my-super-secret-key", "whmcs-killer-api")
	pwdHasher := infrastructure.NewFakePasswordHasher() // 之前寫的假密碼庫，後續可換成 bcrypt

	// 3. 裝配應用層與 Controller
	authApp := application.NewAuthAppService(userRepo, jwtService, pwdHasher)
	authHandler := identityHttp.NewAuthHandler(authApp)

	// 4. 配置 Hertz 路由
	h := server.Default()

	v1 := h.Group("/api/v1")
	{
		v1.POST("/auth/login", authHandler.Login)
	}

	// 傳入真實的 jwtService 給中間件
	privateAPI := h.Group("/api/v1")
	privateAPI.Use(middleware.JWTAuthMiddleware(jwtService))
	{
		// 這裡掛載需要登入的路由...
	}

	h.Spin()
}
