package main

import (
	apiConfig "backend-core/internal/api/config"
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
	nodeGrpc "backend-core/internal/node/interfaces/grpc"
	nodeHttp "backend-core/internal/node/interfaces/http"
	orderingApp "backend-core/internal/ordering/app"
	orderingInfra "backend-core/internal/ordering/infra"
	orderingHttp "backend-core/internal/ordering/interfaces/http"
	productApp "backend-core/internal/product/app"
	productInfra "backend-core/internal/product/infra"
	productHttp "backend-core/internal/product/interfaces/http"
	"backend-core/pkg/agentpb"
	"flag"
	"log"
	"net"
	"os"

	identityHttp "backend-core/internal/identity/interfaces/http"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/glebarez/sqlite"
	"github.com/hertz-contrib/cors"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"gorm.io/gorm"
)

func main() {
	cfgPath := flag.String("config", "api.yaml", "path to API YAML config file")
	flag.Parse()

	// Load config from YAML file; fall back to defaults if file not found
	cfg, err := apiConfig.LoadFromFile(*cfgPath)
	if err != nil {
		log.Printf("[api] could not load config file %s: %v (using defaults)", *cfgPath, err)
		cfg = apiConfig.DefaultConfig()
	}

	// Environment variable overrides
	if v := os.Getenv("API_DATABASE_DSN"); v != "" {
		cfg.Database.DSN = v
	}
	if v := os.Getenv("API_JWT_SECRET"); v != "" {
		cfg.JWT.Secret = v
	}
	if v := os.Getenv("API_AGENT_SECRET"); v != "" {
		cfg.Agent.Secret = v
	}
	if v := os.Getenv("API_GRPC_LISTEN"); v != "" {
		cfg.GRPC.Listen = v
	}

	// 1. Connect to SQLite
	db, err := gorm.Open(sqlite.Open(cfg.Database.DSN), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	// Auto-migrate table schemas
	db.AutoMigrate(&infra.UserPO{})
	db.AutoMigrate(&billingInfra.InvoicePO{}, &billingInfra.LineItemPO{})
	db.AutoMigrate(&orderingInfra.OrderPO{})
	db.AutoMigrate(&instanceInfra.NodePO{}, &instanceInfra.InstancePO{})
	db.AutoMigrate(&productInfra.ProductPO{})
	db.AutoMigrate(&nodeInfra.HostNodePO{}, &nodeInfra.IPAddressPO{}, &nodeInfra.TaskPO{})

	// 2. Wire up infrastructure
	pwdHasher := infra.NewBcryptPasswordService(bcrypt.DefaultCost)
	userRepo := infra.NewSqliteUserRepo(db)
	jwtService := infra.NewJWTService(cfg.JWT.Secret, cfg.JWT.Issuer)

	// 3. Wire up application layer
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
	nApp.SetAgentSecret(cfg.Agent.Secret)
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
		// User profile (returns user_id + role)
		privateAPI.GET("/me", authHandler.Me)

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

	// ---- Admin-only API routes (requires admin role) ----
	adminAPI := h.Group("/api/v1/admin")
	adminAPI.Use(middleware.AdminMiddleware(jwtService))
	{
		// Host Node management
		adminAPI.GET("/host-nodes", nHandler.ListHosts)
		adminAPI.GET("/host-nodes/:id", nHandler.GetHost)
		adminAPI.POST("/host-nodes", nHandler.CreateHost)
		adminAPI.POST("/host-nodes/:id/ips", nHandler.AddIP)
		adminAPI.GET("/host-nodes/:id/ips", nHandler.ListIPs)
		adminAPI.POST("/host-nodes/:id/tasks", nHandler.EnqueueTask)

		// Product management
		adminAPI.POST("/products", prodHandler.Create)
		adminAPI.GET("/products", prodHandler.ListAll)
		adminAPI.POST("/products/:id/enable", prodHandler.Enable)
		adminAPI.POST("/products/:id/disable", prodHandler.Disable)
		adminAPI.PUT("/products/:id/price", prodHandler.UpdatePrice)

		// Instance management (all instances, not just current user)
		adminAPI.POST("/nodes", instHandler.CreateNode)
		adminAPI.POST("/nodes/:id/enable", instHandler.EnableNode)
		adminAPI.POST("/nodes/:id/disable", instHandler.DisableNode)
	}

	// 5. Start gRPC server for agent communication
	grpcServer := grpc.NewServer()
	agentpb.RegisterAgentServiceServer(grpcServer, nodeGrpc.NewAgentGRPCServer(nApp))
	go func() {
		lis, err := net.Listen("tcp", cfg.GRPC.Listen)
		if err != nil {
			log.Fatalf("failed to listen on %s: %v", cfg.GRPC.Listen, err)
		}
		log.Printf("[api] gRPC agent server listening on %s", cfg.GRPC.Listen)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("gRPC server failed: %v", err)
		}
	}()

	h.Spin()
}
