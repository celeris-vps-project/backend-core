package main

import (
	billingApp "backend-core/internal/billing/app"
	billingInfra "backend-core/internal/billing/infra"
	billingHttp "backend-core/internal/billing/interfaces/http"
	"backend-core/internal/identity/app"
	"backend-core/internal/identity/infra"
	"backend-core/internal/identity/interfaces/http/middleware"
	instanceApp "backend-core/internal/instance/app"
	instanceInfra "backend-core/internal/instance/infra"
	instanceHttp "backend-core/internal/instance/interfaces/http"
	nodeApp "backend-core/internal/node/app"
	nodeInfra "backend-core/internal/node/infra"
	nodeHttp "backend-core/internal/node/interfaces/http"
	orderingApp "backend-core/internal/ordering/app"
	orderingInfra "backend-core/internal/ordering/infra"
	orderingHttp "backend-core/internal/ordering/interfaces/http"
	productApp "backend-core/internal/product/app"
	productInfra "backend-core/internal/product/infra"
	productHttp "backend-core/internal/product/interfaces/http"
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
	db.AutoMigrate(&billingInfra.InvoicePO{}, &billingInfra.LineItemPO{})
	db.AutoMigrate(&orderingInfra.OrderPO{})
	db.AutoMigrate(&instanceInfra.NodePO{}, &instanceInfra.InstancePO{})
	db.AutoMigrate(&productInfra.ProductPO{})
	db.AutoMigrate(&nodeInfra.HostNodePO{}, &nodeInfra.IPAddressPO{}, &nodeInfra.TaskPO{})

	// 2. 實例化真實的基礎設施
	pwdHasher := infra.NewBcryptPasswordService(bcrypt.DefaultCost)
	userRepo := infra.NewSqliteUserRepo(db)
	jwtService := infra.NewJWTService("my-super-secret-key", "whmcs-killer-api")

	// 3. 裝配應用層與 Controller
	authApp := app.NewAuthAppService(userRepo, jwtService, pwdHasher)
	authHandler := identityHttp.NewAuthHandler(authApp)

	// Billing
	invoiceRepo := billingInfra.NewSqliteInvoiceRepo(db)
	idGen := billingInfra.NewUUIDGenerator()
	invoiceApp := billingApp.NewInvoiceAppService(invoiceRepo, idGen, nil) // gateway = nil for now
	invoiceHandler := billingHttp.NewInvoiceHandler(invoiceApp)

	// Ordering
	orderRepo := orderingInfra.NewSqliteOrderRepo(db)
	orderApp := orderingApp.NewOrderAppService(orderRepo, idGen, nil) // provisioning = nil for now
	orderHandler := orderingHttp.NewOrderHandler(orderApp)

	// Instance
	nodeRepo := instanceInfra.NewSqliteNodeRepo(db)
	instRepo := instanceInfra.NewSqliteInstanceRepo(db)
	instApp := instanceApp.NewInstanceAppService(nodeRepo, instRepo, idGen)
	instHandler := instanceHttp.NewInstanceHandler(instApp)

	// Product
	prodRepo := productInfra.NewSqliteProductRepo(db)
	prodApp := productApp.NewProductAppService(prodRepo, idGen)
	prodHandler := productHttp.NewProductHandler(prodApp)

	// Node (host machines, IP pools, agent tasks)
	hostRepo := nodeInfra.NewSqliteHostNodeRepo(db)
	ipRepo := nodeInfra.NewSqliteIPAddressRepo(db)
	taskRepo := nodeInfra.NewSqliteTaskRepo(db)
	nApp := nodeApp.NewNodeAppService(hostRepo, ipRepo, taskRepo, idGen)
	nHandler := nodeHttp.NewNodeHandler(nApp)

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

		// Public - browse available locations & nodes
		v1.GET("/locations", instHandler.ListLocations)
		v1.GET("/nodes", instHandler.ListNodes)
		v1.GET("/nodes/:id", instHandler.GetNode)

		// Public - product catalog
		v1.GET("/products", prodHandler.List)
		v1.GET("/products/:id", prodHandler.GetByID)

		// Public - host node info
		v1.GET("/host-nodes", nHandler.ListHosts)
		v1.GET("/host-nodes/:id", nHandler.GetHost)

		// Agent endpoints (authenticated by shared secret, not JWT)
		v1.POST("/agent/register", nHandler.AgentRegister)
		v1.POST("/agent/heartbeat", nHandler.AgentHeartbeat)
		v1.POST("/agent/tasks/result", nHandler.AgentTaskResult)
	}

	// 傳入真實的 jwtService 給中間件
	privateAPI := h.Group("/api/v1")
	privateAPI.Use(middleware.JWTAuthMiddleware(jwtService))
	{
		// Billing - Invoice routes
		privateAPI.POST("/invoices", invoiceHandler.Create)
		privateAPI.GET("/invoices", invoiceHandler.ListByCustomer)
		privateAPI.GET("/invoices/:id", invoiceHandler.GetByID)
		privateAPI.POST("/invoices/:id/line-items", invoiceHandler.AddLineItem)
		privateAPI.PUT("/invoices/:id/tax", invoiceHandler.SetTax)
		privateAPI.POST("/invoices/:id/issue", invoiceHandler.Issue)
		privateAPI.POST("/invoices/:id/payments", invoiceHandler.RecordPayment)
		privateAPI.POST("/invoices/:id/void", invoiceHandler.Void)

		// Ordering - Order routes
		privateAPI.POST("/orders", orderHandler.Create)
		privateAPI.GET("/orders", orderHandler.ListByCustomer)
		privateAPI.GET("/orders/:id", orderHandler.GetByID)
		privateAPI.POST("/orders/:id/activate", orderHandler.Activate)
		privateAPI.POST("/orders/:id/suspend", orderHandler.Suspend)
		privateAPI.POST("/orders/:id/unsuspend", orderHandler.Unsuspend)
		privateAPI.POST("/orders/:id/cancel", orderHandler.Cancel)
		privateAPI.POST("/orders/:id/terminate", orderHandler.Terminate)

		// Instance - Node admin routes
		privateAPI.POST("/nodes", instHandler.CreateNode)
		privateAPI.POST("/nodes/:id/enable", instHandler.EnableNode)
		privateAPI.POST("/nodes/:id/disable", instHandler.DisableNode)

		// Instance - Customer routes
		privateAPI.POST("/instances", instHandler.Purchase)
		privateAPI.GET("/instances", instHandler.ListByCustomer)
		privateAPI.GET("/instances/:id", instHandler.GetByID)
		privateAPI.POST("/instances/:id/start", instHandler.Start)
		privateAPI.POST("/instances/:id/stop", instHandler.Stop)
		privateAPI.POST("/instances/:id/suspend", instHandler.Suspend)
		privateAPI.POST("/instances/:id/unsuspend", instHandler.Unsuspend)
		privateAPI.POST("/instances/:id/terminate", instHandler.Terminate)
		privateAPI.PUT("/instances/:id/ip", instHandler.AssignIP)

		// Product - Admin routes
		privateAPI.POST("/products", prodHandler.Create)
		privateAPI.GET("/products/all", prodHandler.ListAll)
		privateAPI.POST("/products/:id/enable", prodHandler.Enable)
		privateAPI.POST("/products/:id/disable", prodHandler.Disable)
		privateAPI.PUT("/products/:id/price", prodHandler.UpdatePrice)

		// Host Node - Admin routes
		privateAPI.POST("/host-nodes", nHandler.CreateHost)
		privateAPI.POST("/host-nodes/:id/ips", nHandler.AddIP)
		privateAPI.GET("/host-nodes/:id/ips", nHandler.ListIPs)
		privateAPI.POST("/host-nodes/:id/tasks", nHandler.EnqueueTask)
	}

	h.Spin()
}
