package main

import (
	"backend-core/internal/identity/app"
	"backend-core/internal/identity/infra"
	"backend-core/internal/identity/interfaces/http/middleware"
	"log"

	identityHttp "backend-core/internal/identity/interfaces/http"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/glebarez/sqlite"
	"github.com/hertz-contrib/cors"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func main() {
	// 1. 連接 SQLite (請替換為你的真實路徑)
	dsn := "data.db"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("無法連接資料庫: %v", err)
	}

	// (可選) 自動遷移表結構
	db.AutoMigrate(&infra.UserPO{})

	// 2. 實例化真實的基礎設施
	pwdHasher := infra.NewBcryptPasswordService(bcrypt.DefaultCost)
	userRepo := infra.NewSqliteUserRepo(db)
	jwtService := infra.NewJWTService("my-super-secret-key", "whmcs-killer-api")

	// 3. 裝配應用層與 Controller
	authApp := app.NewAuthAppService(userRepo, jwtService, pwdHasher)
	authHandler := identityHttp.NewAuthHandler(authApp)

	// 4. 配置 Hertz 路由
	h := server.Default()
	h.Use(cors.New(cors.Config{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"PUT", "PATCH", "POST", "GET", "DELETE"},
		AllowHeaders: []string{"Origin", "Authorization", "Content-Type"},
	}))

	v1 := h.Group("/api/v1")

	{
		v1.POST("/auth/register", authHandler.Register)
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
