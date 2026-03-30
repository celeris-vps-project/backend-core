package main

import (
	apiConfig "backend-core/internal/api/config"
	billingApp "backend-core/internal/billing/app"
	billingInfra "backend-core/internal/billing/infra"
	billingHttp "backend-core/internal/billing/interfaces/http"
	catalogApp "backend-core/internal/catalog/app"
	catalogInfra "backend-core/internal/catalog/infra"
	catalogHttp "backend-core/internal/catalog/interfaces/http"
	checkoutAppPkg "backend-core/internal/checkout/app"
	checkoutInfra "backend-core/internal/checkout/infra"
	checkoutHttp "backend-core/internal/checkout/interfaces/http"
	"backend-core/internal/identity/app"
	"backend-core/internal/identity/infra"
	"backend-core/internal/identity/interfaces/http/middleware"
	instanceApp "backend-core/internal/instance/app"
	instanceDomain "backend-core/internal/instance/domain"
	instanceInfra "backend-core/internal/instance/infra"
	instanceHttp "backend-core/internal/instance/interfaces/http"
	instanceWs "backend-core/internal/instance/interfaces/ws"
	orderingApp "backend-core/internal/ordering/app"
	orderingInfra "backend-core/internal/ordering/infra"
	orderingHttp "backend-core/internal/ordering/interfaces/http"
	paymentApp "backend-core/internal/payment/app"
	paymentDomain "backend-core/internal/payment/domain"
	paymentInfra "backend-core/internal/payment/infra"
	paymentHttp "backend-core/internal/payment/interfaces/http"
	provisioningApp "backend-core/internal/provisioning/app"
	provisioningInfra "backend-core/internal/provisioning/infra"
	provisioningGrpc "backend-core/internal/provisioning/interfaces/grpc"
	provisioningHttp "backend-core/internal/provisioning/interfaces/http"
	provisioningWs "backend-core/internal/provisioning/interfaces/ws"
	"backend-core/internal/web"
	"backend-core/pkg/adaptive"
	"backend-core/pkg/agentpb"
	"backend-core/pkg/contracts"
	"backend-core/pkg/circuitbreaker"
	"backend-core/pkg/database"
	"backend-core/pkg/delayed"
	"backend-core/pkg/eventbus"
	"backend-core/pkg/events"
	"backend-core/pkg/messageclient"
	"backend-core/pkg/perf"
	"backend-core/pkg/ratelimit"
	"backend-core/pkg/timeout"
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	identityHttp "backend-core/internal/identity/interfaces/http"

	hertzApp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/adaptor"
	"github.com/hertz-contrib/cors"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
)

var version = "dev"

func main() {
	log.Printf("[api] Celeris API %s", version)
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
	if v := os.Getenv("API_GRPC_LISTEN"); v != "" {
		cfg.GRPC.Listen = v
	}
	if v := os.Getenv("API_MESSAGE_ADDRESS"); v != "" {
		cfg.Message.Address = v
	}
	if v := os.Getenv("API_MESSAGE_SERVICE_TOKEN"); v != "" {
		cfg.Message.ServiceToken = v
	}
	if v := os.Getenv("API_MESSAGE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Message.Timeout = d
		} else {
			log.Printf("[api] invalid API_MESSAGE_TIMEOUT %q: %v", v, err)
		}
	}

	// 1. Open database (SQLite or PostgreSQL, based on config)
	db, err := database.Open(cfg.Database)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	// Auto-migrate table schemas
	db.AutoMigrate(&infra.UserPO{})
	db.AutoMigrate(&billingInfra.InvoicePO{}, &billingInfra.LineItemPO{})
	db.AutoMigrate(&orderingInfra.OrderPO{})
	db.AutoMigrate(&instanceInfra.InstancePO{})
	db.AutoMigrate(&catalogInfra.ProductPO{})
	db.AutoMigrate(&provisioningInfra.RegionPO{}, &provisioningInfra.HostNodePO{}, &provisioningInfra.IPAddressPO{}, &provisioningInfra.TaskPO{}, &provisioningInfra.ResourcePoolPO{}, &provisioningInfra.BootstrapTokenPO{})
	db.AutoMigrate(&paymentInfra.PaymentProviderPO{})

	// 2. Wire up infrastructure
	pwdHasher := infra.NewBcryptPasswordService(bcrypt.DefaultCost)
	userRepo := infra.NewGormUserRepo(db)
	jwtService := infra.NewJWTService(cfg.JWT.Secret, cfg.JWT.Issuer)
	var msgClient *messageclient.Client
	var registrationNotifier app.RegistrationNotifier
	if cfg.Message.Enabled() {
		msgClient, err = messageclient.Dial(messageclient.Config{
			Address:      cfg.Message.Address,
			ServiceToken: cfg.Message.ServiceToken,
			Timeout:      cfg.Message.Timeout,
		})
		if err != nil {
			log.Fatalf("[api] failed to initialise message client: %v", err)
		}
		registrationNotifier = messageclient.NewUserRegisteredNotifier(msgClient, messageclient.UserRegisteredNotifierConfig{
			Enabled:      cfg.Message.UserRegistered.Enabled,
			Channel:      cfg.Message.UserRegistered.Channel,
			Subject:      cfg.Message.UserRegistered.Subject,
			Content:      cfg.Message.UserRegistered.Content,
			TemplateCode: cfg.Message.UserRegistered.TemplateCode,
		})
		log.Printf("[api] message service integration enabled: address=%s user_registered=%t channel=%s",
			cfg.Message.Address,
			cfg.Message.UserRegistered.Enabled,
			cfg.Message.UserRegistered.Channel,
		)
	} else {
		log.Printf("[api] message service integration disabled (set message.address and message.service_token to enable)")
	}

	// 3. Wire up application layer
	authApp := app.NewAuthAppService(userRepo, jwtService, pwdHasher, registrationNotifier)
	authHandler := identityHttp.NewAuthHandler(authApp)

	// ── Admin Account Seeding ──────────────────────────────────────────────
	// On first run, creates an admin account with a random 12-char password
	// that is printed to the log exactly once. Subsequent starts skip this.
	adminEmail := cfg.Admin.Email
	if adminEmail == "" {
		adminEmail = "admin@celeris.local"
	}
	authApp.EnsureAdmin(context.Background(), adminEmail)

	// Billing
	invoiceRepo := billingInfra.NewGormInvoiceRepo(db)
	idGen := billingInfra.NewUUIDGenerator()
	invoiceApp := billingApp.NewInvoiceAppService(invoiceRepo, idGen, nil) // gateway = nil for now
	invoiceHandler := billingHttp.NewInvoiceHandler(invoiceApp)

	// Ordering
	orderRepo := orderingInfra.NewGormOrderRepo(db)
	orderApp := orderingApp.NewOrderAppService(orderRepo, idGen)
	orderHandler := orderingHttp.NewOrderHandler(orderApp)

	// Payment — USDT crypto payment provider (loaded from config YAML)
	// Config source: crypto section in api.yaml (see api.example.yaml)
	// Environment override: CRYPTO_MOCK_MODE=false forces production mode.
	cryptoCfg := paymentInfra.DefaultCryptoConfig()

	// Apply YAML config values
	if len(cfg.Crypto.Wallets) > 0 {
		wallets := make(map[paymentDomain.CryptoNetwork]string)
		for network, addr := range cfg.Crypto.Wallets {
			wallets[paymentDomain.CryptoNetwork(network)] = addr
		}
		cryptoCfg.Wallets = wallets
	}
	cryptoCfg.MockMode = cfg.Crypto.MockMode
	if d, err := time.ParseDuration(cfg.Crypto.PaymentTimeout); err == nil && d > 0 {
		cryptoCfg.PaymentTimeout = d
	}
	if d, err := time.ParseDuration(cfg.Crypto.MockConfirmDelay); err == nil && d > 0 {
		cryptoCfg.MockConfirmDelay = d
	}

	// Environment variable override takes precedence over YAML
	if v := os.Getenv("CRYPTO_MOCK_MODE"); v == "false" || v == "0" {
		cryptoCfg.MockMode = false
	} else if v == "true" || v == "1" {
		cryptoCfg.MockMode = true
	}

	// Event Bus — in-process synchronous bus for domain event integration
	bus := eventbus.New()

	// Provisioning (host machines, IP pools, agent tasks, resource pools, bootstrap tokens)
	hostRepo := provisioningInfra.NewGormHostNodeRepo(db)
	ipRepo := provisioningInfra.NewGormIPAddressRepo(db)
	taskRepo := provisioningInfra.NewGormTaskRepo(db)
	regionRepo := provisioningInfra.NewGormRegionRepo(db)
	poolRepo := provisioningInfra.NewGormResourcePoolRepo(db)
	btRepo := provisioningInfra.NewGormBootstrapTokenRepo(db)
	nodeStateCache := provisioningInfra.NewMemoryNodeStateCache(60 * time.Second)
	provSvc := provisioningApp.NewProvisioningAppService(hostRepo, ipRepo, taskRepo, regionRepo, poolRepo, btRepo, nodeStateCache, idGen, bus)
	nHandler := provisioningHttp.NewNodeHandler(provSvc)

	// Catalog (products with event-driven provisioning & physical capacity checking)
	//
	// Decorator chain (outer → inner): Bloom → Singleflight → Gorm
	//   1. Bloom filter:   O(1) bit-check rejects definitely-nonexistent IDs instantly
	//                      → blocks cache penetration attacks with random fake IDs
	//   2. Singleflight:   deduplicates concurrent reads for the SAME key
	//                      → prevents thundering herd on hot products
	//   3. Gorm:           actual database query
	//
	// The cheapest filter is outermost so attack traffic never reaches
	// the more expensive layers (singleflight map ops, DB queries).
	gormProdRepo := catalogInfra.NewGormProductRepo(db)
	sfProdRepo := catalogInfra.NewSingleflightProductRepo(gormProdRepo)
	prodRepo := catalogInfra.NewBloomProductRepo(sfProdRepo, 10000, 0.01)
	capacityCheckerRaw := catalogInfra.NewNodeCapacityAdapter(hostRepo)
	capacityChecker := catalogInfra.NewNodeCapacityAdapterWithCB(capacityCheckerRaw,
		circuitbreaker.New("node-capacity", 3, 2, 15*time.Second))
	prodApp := catalogApp.NewProductAppService(prodRepo, idGen, bus, capacityChecker)
	prodHandler := catalogHttp.NewProductHandler(prodApp)

	// Product Line (customer-facing product browsing by resource pool)
	// Uses a catalog/infra adapter so the handler never imports provisioning directly.
	// Wrapped with circuit breaker + cache fallback for graceful degradation.
	plDataSourceRaw := catalogInfra.NewProvisioningProductLineAdapter(provSvc)
	plDataSource := catalogInfra.NewProductLineAdapterWithCB(plDataSourceRaw,
		circuitbreaker.New("product-line", 5, 2, 30*time.Second))
	productLineHandler := catalogHttp.NewProductLineHandler(prodApp, plDataSource)

	// ── Delayed Event Infrastructure ───────────────────────────────────────
	// The delayed router and publisher must be initialised before the
	// VPSProvisioner so we can inject the publisher for async boot
	// confirmation scheduling.

	// Delayed event router — topic-based dispatcher shared across bounded contexts.
	// Each context registers its own handler; the router dispatches by topic.
	// NOTE: boot confirmation and invoice.check_timeout handlers are registered
	// below after their dependencies are created (resolves dependency order).
	delayedRouter := delayed.NewRouter()

	// Delayed event publisher — in-memory timer-based implementation.
	// Suitable for single-instance; replace with Asynq (Redis) for production.
	//
	// To switch to Asynq (production):
	//   delayedPublisher := delayed.NewAsynqPublisher(asynq.RedisClientOpt{Addr: redisAddr})
	//   delayedConsumer  := delayed.NewAsynqConsumer(asynq.RedisClientOpt{Addr: redisAddr}, delayedRouter)
	//   go delayedConsumer.Start(context.Background())
	delayedPublisher := delayed.NewInMemoryPublisher(delayedRouter.Dispatch)

	// Boot confirmation worker — handles delayed "provision.confirm_boot" events.
	// Now upgraded: emits events, retries with exponential backoff, and marks
	// stuck tasks as failed. Receives the delayedPublisher for re-scheduling retries.
	bootConfirmWorker := provisioningInfra.NewBootConfirmationWorker(taskRepo, nodeStateCache, bus, delayedPublisher)
	delayedRouter.Handle("provision.confirm_boot", bootConfirmWorker.HandlerFunc())

	// Provision Dispatcher - routes provisioning commands by product type
	// VPSProvisioner receives the delayed publisher for async boot confirmation.
	//
	// MockMode: when true, tasks are auto-completed without a real agent.
	// Set to false (or remove WithMockMode) when real agents are connected.
	// Env override: PROVISION_MOCK_MODE=false disables mock mode.
	provisionMockMode := true // default: mock mode for dev (no real agent)
	if v := os.Getenv("PROVISION_MOCK_MODE"); v == "false" || v == "0" {
		provisionMockMode = false
	}
	vpsProvisioner := provisioningApp.NewVPSProvisioner(hostRepo, poolRepo, taskRepo, idGen,
		provisioningApp.WithDelayedPublisher(delayedPublisher),
		provisioningApp.WithIPRepo(ipRepo),
		provisioningApp.WithStateCache(nodeStateCache),
		provisioningApp.WithMockMode(provisionMockMode),
	)
	provDispatcher := provisioningApp.NewProvisionDispatcher("vps")
	provDispatcher.Register(vpsProvisioner)
	provDispatcher.RegisterEventHandlers(bus)
	if provisionMockMode {
		log.Printf("[api] VPS provisioner: MOCK MODE (tasks auto-completed, set PROVISION_MOCK_MODE=false for real agents)")
	} else {
		log.Printf("[api] VPS provisioner: PRODUCTION MODE (tasks queued for real agents)")
	}
	log.Printf("[api] boot confirmation queue enabled (InMemory, delay=30s)")

	// WebSocket Hub - broadcasts real-time node state to admin clients
	wsHub := provisioningWs.NewHub()
	wsHub.Register(bus)

	// Instance (uses adapter to delegate node capacity to the provisioning module)
	nodeAllocatorRepo := instanceInfra.NewHostNodeAllocatorAdapter(hostRepo)
	instRepo := instanceInfra.NewGormInstanceRepo(db)
	instanceWSHub := instanceWs.NewHub(instRepo)
	instanceWSHub.Register(bus)

	// Provisioning Bus — in-memory channel-based queue that throttles concurrent
	// provisioning requests. The handler callback is a placeholder (mock log);
	// replace with real agent dispatch when agents are integrated.
	// Swap for RabbitMQProvisioningBus / NoopProvisioningBus via config in the future.
	provisioningBus := instanceInfra.NewChannelProvisioningBus(
		func(req instanceDomain.ProvisionRequest) {
			log.Printf("[ProvisioningBus] processing provision request: instance=%s node=%s order=%s hostname=%s",
				req.InstanceID, req.NodeID, req.OrderID, req.Hostname)
			// TODO: dispatch to real agent or enqueue task via node domain
		},
	)
	provisioningBus.Start(context.Background())

	instApp := instanceApp.NewInstanceAppService(nodeAllocatorRepo, instRepo, idGen, provisioningBus)
	instApp.SetLifecycleScheduler(instanceInfra.NewProvisioningTaskScheduler(provSvc))
	instApp.SetEventPublisher(bus)
	instHandler := instanceHttp.NewInstanceHandler(instApp)

	// ── Provisioning → Instance Event Bridge ───────────────────────────────
	// Subscribe to provisioning domain events and update instance state.
	// When a VM is successfully provisioned (agent reports back with IP),
	// the instance is automatically transitioned from "pending" to "running"
	// with the assigned internal IP and NAT port.
	bus.Subscribe("node.provisioning_completed", func(evt eventbus.Event) {
		e, ok := evt.(events.ProvisioningCompletedEvent)
		if !ok {
			return
		}
		log.Printf("[event-bridge] provisioning completed: instance=%s ipv4=%s nat_port=%d",
			e.InstanceID, e.IPv4, e.NATPort)
		if err := instApp.ConfirmProvisioning(e.InstanceID, e.NodeID, e.IPv4, e.IPv6, e.NetworkMode, e.NATPort); err != nil {
			log.Printf("[event-bridge] ERROR: failed to confirm provisioning for instance %s: %v", e.InstanceID, err)
		}
	})
	bus.Subscribe("node.provisioning_failed", func(evt eventbus.Event) {
		e, ok := evt.(events.ProvisioningFailedEvent)
		if !ok {
			return
		}
		log.Printf("[event-bridge] provisioning failed: instance=%s error=%s", e.InstanceID, e.Error)
		if err := vpsProvisioner.Release(provisioningApp.ProvisionCommand{
			InstanceID: e.InstanceID,
			NodeID:     e.NodeID,
		}); err != nil {
			log.Printf("[event-bridge] ERROR: failed to release resources for failed instance %s: %v", e.InstanceID, err)
		}
		// Instance stays in "pending" state — admin can investigate and retry
	})
	bus.Subscribe("node.instance_task_completed", func(evt eventbus.Event) {
		e, ok := evt.(events.InstanceTaskCompletedEvent)
		if !ok {
			return
		}
		switch contracts.TaskType(e.TaskType) {
		case contracts.TaskStart, contracts.TaskReboot:
			if err := instApp.ConfirmStarted(e.InstanceID); err != nil {
				log.Printf("[event-bridge] ERROR: failed to confirm start for instance %s: %v", e.InstanceID, err)
			}
		case contracts.TaskStop:
			if err := instApp.ConfirmStopped(e.InstanceID); err != nil {
				log.Printf("[event-bridge] ERROR: failed to confirm stop for instance %s: %v", e.InstanceID, err)
			}
		case contracts.TaskSuspend:
			if err := instApp.ConfirmSuspended(e.InstanceID); err != nil {
				log.Printf("[event-bridge] ERROR: failed to confirm suspend for instance %s: %v", e.InstanceID, err)
			}
		case contracts.TaskUnsuspend:
			if err := instApp.ConfirmUnsuspended(e.InstanceID); err != nil {
				log.Printf("[event-bridge] ERROR: failed to confirm unsuspend for instance %s: %v", e.InstanceID, err)
			}
		case contracts.TaskDeprovision:
			if err := vpsProvisioner.Release(provisioningApp.ProvisionCommand{
				InstanceID: e.InstanceID,
				NodeID:     e.NodeID,
			}); err != nil {
				log.Printf("[event-bridge] ERROR: failed to release resources for terminated instance %s: %v", e.InstanceID, err)
				return
			}
			if err := instApp.ConfirmTerminated(e.InstanceID); err != nil {
				log.Printf("[event-bridge] ERROR: failed to confirm termination for instance %s: %v", e.InstanceID, err)
			}
		}
	})
	bus.Subscribe("node.instance_task_failed", func(evt eventbus.Event) {
		e, ok := evt.(events.InstanceTaskFailedEvent)
		if !ok {
			return
		}
		log.Printf("[event-bridge] instance task failed: instance=%s type=%s error=%s", e.InstanceID, e.TaskType, e.Error)
	})
	log.Printf("[api] provisioning → instance event bridge enabled")

	// ── Provision Poller (background worker) ───────────────────────────────
	// Periodically checks pending/running tasks and marks stale ones as failed.
	// Acts as a safety net in case agent callbacks are lost.
	provPollerCtx, provPollerCancel := context.WithCancel(context.Background())
	provPoller := provisioningInfra.NewProvisionPoller(taskRepo, nodeStateCache, bus, provisioningInfra.ProvisionPollerConfig{
		PollInterval:   10 * time.Second,
		StaleThreshold: 10 * time.Minute,
	})
	provPoller.Start(provPollerCtx)
	log.Printf("[api] provision poller started (10s interval, 10min stale threshold)")

	// Payment handler — thin HTTP layer that delegates cross-domain work to the orchestrator.
	// Build adapters that implement the orchestrator's ports without importing other contexts' domain types.
	// Each adapter is wrapped with a circuit breaker for fault isolation and graceful degradation.
	//
	// ── Circuit Breaker Configuration ──────────────────────────────────────
	//   ordering  : 5 failures / 30s timeout — FAST-FAIL (critical path)
	//   catalog   : 5 failures / 30s timeout — FAST-FAIL (slot consumption)
	//   instance  : 3 failures / 20s timeout — SILENT DEGRADATION (non-fatal)
	orderAdapterRaw := paymentInfra.NewOrderingAdapter(orderApp)
	orderAdapter := paymentInfra.NewOrderingAdapterWithCB(orderAdapterRaw,
		circuitbreaker.New("pay-ordering", 5, 2, 30*time.Second))

	catalogAdapterRaw := paymentInfra.NewCatalogAdapter(prodApp)
	catalogAdapter := paymentInfra.NewCatalogAdapterWithCB(catalogAdapterRaw,
		circuitbreaker.New("pay-catalog", 5, 2, 30*time.Second))

	instanceAdapterRaw := paymentInfra.NewInstanceAdapter(instApp)
	instanceAdapter := paymentInfra.NewInstanceAdapterWithCB(instanceAdapterRaw,
		circuitbreaker.New("pay-instance", 3, 2, 20*time.Second))

	// Billing adapter — wraps the billing app service for the payment orchestrator.
	// Creates invoices at Pay time, records payments at webhook time.
	billingAdapterRaw := paymentInfra.NewBillingAdapter(invoiceApp)
	billingAdapter := paymentInfra.NewBillingAdapterWithCB(billingAdapterRaw,
		circuitbreaker.New("pay-billing", 3, 2, 20*time.Second))

	postPayOrch := paymentApp.NewPostPaymentOrchestrator(orderAdapter, catalogAdapter, instanceAdapter, billingAdapter, delayedPublisher)
	renewalSvc := paymentApp.NewRenewalService(orderAdapter, billingAdapter, instanceAdapter)

	// Invoice timeout worker — handles delayed "invoice.check_timeout" events.
	// Now delegates to the orchestrator (via ports) instead of direct cross-context imports.
	invoiceTimeoutWorker := paymentInfra.NewInvoiceTimeoutWorker(postPayOrch)
	delayedRouter.Handle("invoice.check_timeout", invoiceTimeoutWorker.HandlerFunc())

	// Payment Provider management — dynamic provider configuration via admin UI
	providerRepo := paymentInfra.NewGormPaymentProviderRepo(db)
	providerSvc := paymentApp.NewProviderAppService(providerRepo, idGen)
	// Register provider factories — keeps payment/app free of payment/infra imports.
	providerSvc.RegisterFactory(paymentDomain.ProviderTypeEPay, func(cfg *paymentDomain.PaymentProviderConfig, cb func(*paymentDomain.WebhookPayload)) paymentDomain.PaymentProvider {
		return paymentInfra.NewEPayPaymentProvider(cfg, cb)
	})
	// Register notify URL builder — used by ProviderAppService to auto-fill EPay config.
	// Builds an absolute URL using the configured server domain so that external
	// payment gateways (EPay) can call back to this server.
	providerSvc.RegisterNotifyURLBuilder(func(providerID string) string {
		domain := cfg.Server.Domain
		port := cfg.Server.Port.String()
		scheme := "https"
		if domain == "localhost" || domain == "127.0.0.1" {
			scheme = "http"
		}
		host := domain
		if port != "443" && port != "80" && port != "" {
			host = host + ":" + port
		}
		return scheme + "://" + host + "/api/v1/payments/webhook/epay/" + providerID
	})
	cryptoProvider := paymentInfra.NewCryptoPaymentProvider(&cryptoCfg, nil) // callback set below

	// PaymentAppService now owns all business logic: provider routing, invoice
	// creation, timeout scheduling, and webhook handling.
	paySvc := paymentApp.NewPaymentAppService(providerSvc, postPayOrch, cryptoProvider)
	paySvc.SetRenewalService(renewalSvc)
	// PaymentHandler is now a thin HTTP adapter — only parses/serialises.
	payHandler := paymentHttp.NewPaymentHandler(paySvc)
	providerHandler := paymentHttp.NewProviderHandler(providerSvc)
	log.Printf("[api] payment provider management enabled (dynamic admin configuration)")
	log.Printf("[api] payment orchestrator circuit breakers enabled (ordering=5/30s, catalog=5/30s, instance=3/20s)")
	if cryptoCfg.MockMode {
		log.Printf("[api] USDT crypto payment: MOCK MODE (auto-confirms after %v, set CRYPTO_MOCK_MODE=false for real blockchain)", cryptoCfg.MockConfirmDelay)
	} else {
	log.Printf("[api] USDT crypto payment: PRODUCTION MODE (awaiting real blockchain confirmations via webhook)")
	}
	log.Printf("[api]   payment timeout: %v, wallets configured: %d networks", cryptoCfg.PaymentTimeout, len(cryptoCfg.Wallets))

	renewalPollInterval, err := time.ParseDuration(cfg.Billing.RenewalPollInterval)
	if err != nil {
		log.Printf("[api] invalid billing.renewal_poll_interval %q: %v (using 1h)", cfg.Billing.RenewalPollInterval, err)
		renewalPollInterval = time.Hour
	}
	renewalWorkerCtx, renewalWorkerCancel := context.WithCancel(context.Background())
	renewalWorker := paymentInfra.NewRenewalWorker(renewalSvc, cfg.Billing.RenewalIssueLeadDays, renewalPollInterval)
	renewalWorker.Start(renewalWorkerCtx)
	log.Printf("[api] renewal worker started (lead_days=%d interval=%v)", cfg.Billing.RenewalIssueLeadDays, renewalPollInterval)

	// Wire the crypto provider's async webhook callback to the app service.
	// When payment is confirmed (mock auto-confirm or real blockchain callback),
	// it calls paySvc.HandleWebhookPayload to activate the order and trigger provisioning.
	cryptoProvider.SetCallback(paySvc.HandleWebhookPayload)
	// Also set the callback on providerSvc so factory-built providers can use it.
	providerSvc.SetCallback(paySvc.HandleWebhookPayload)

	// Region handler (using ProvisioningAppService)
	rHandler := provisioningHttp.NewRegionHandler(provSvc)

	// ── Unified Checkout Module ────────────────────────────────────────────
	// Adaptive sync/async checkout that delegates to real product + ordering
	// domains. Uses pkg/adaptive for QPS-based switching. This replaces the
	// need for a separate flash-sale page — any product checkout benefits
	// from automatic async downgrade under high load.
	//
	// Architecture:
	//   POST /checkout → adaptive.Dispatcher → SyncProcessor (QPS < 500)
	//                                        → AsyncProcessor (QPS >= 500)
	//   SyncProcessor  → CheckoutAppService.Execute() → product.PurchaseProduct + ordering.CreateOrder → 200
	//   AsyncProcessor → enqueue → background worker → CheckoutAppService.Execute() → 202
	coStatusStore := checkoutInfra.NewOrderStatusStore()
	coAppSvc := checkoutAppPkg.NewCheckoutAppService(prodApp, orderApp)
	coSyncProc := checkoutInfra.NewSyncCheckoutProcessor(coAppSvc)
	coAsyncProc := checkoutInfra.NewAsyncCheckoutProcessor(coAppSvc, coStatusStore)
	coAsyncProc.Start(context.Background())
	coQPSMonitor := adaptive.NewSlidingWindowQPSMonitor(10) // 10-second window
	coDispatcher := adaptive.NewDispatcher(coSyncProc, coAsyncProc, coQPSMonitor, 500)
	coHandler := checkoutHttp.NewCheckoutHandler(coDispatcher, coStatusStore)
	log.Printf("[api] unified checkout module initialised (adaptive, threshold=500 QPS)")

	// ── Adaptive Cache for Catalog Reads ───────────────────────────────────
	// QPS-driven caching for public catalog GET endpoints (products, groups,
	// regions, resource-pools, nodes). Under normal load, requests pass
	// through to the DB for fresh data. Under high load (QPS >= threshold),
	// responses are served from an in-memory cache with a short TTL.
	//
	// This protects the database during traffic spikes (flash sales,
	// promotions) while ensuring fresh data under normal conditions.
	catalogMonitor := adaptive.NewSlidingWindowQPSMonitor(10) // 10-second window
	catalogCache := adaptive.CacheMiddleware(catalogMonitor, 200, 5*time.Second)
	log.Printf("[api] adaptive catalog cache enabled (threshold=200 QPS, TTL=5s)")

	// ── Performance Tracker ────────────────────────────────────────────────
	// Global per-endpoint performance monitoring with sliding window metrics.
	// The middleware records every request; the PerformanceHub broadcasts
	// snapshots over WebSocket to the admin Performance dashboard.
	perfTracker := perf.NewEndpointTracker(30)           // 30-second sliding window
	perfHub := perf.NewPerformanceHub(perfTracker, 2, 5) // 2s interval, top 5
	log.Printf("[api] performance tracker initialised (30s window, top-5 endpoints)")

	// ── Tiered Rate Limiters (Token Bucket) ────────────────────────────────
	// Instead of a single global rate limiter, we create separate limiters
	// for different endpoint tiers based on their traffic patterns and
	// security requirements:
	//
	//   Baseline  — loose safety-net on ALL endpoints (very permissive)
	//   Critical  — public catalog reads (products, groups, regions)
	//   Checkout  — purchase/payment writes (strict per-IP)
	//   Auth      — login/register (very strict per-IP, anti brute-force)
	//   Standard  — general authenticated business endpoints
	//   Admin     — admin endpoints (RBAC-protected, relaxed limits)
	//
	// Agent and webhook endpoints are intentionally NOT rate-limited to avoid
	// dropping heartbeats or payment callbacks.
	rlCfg := cfg.RateLimit

	// Baseline: applied globally as a loose safety net
	var baselineRL *ratelimit.RateLimiter
	if rlCfg.Baseline.GlobalQPS > 0 || rlCfg.Baseline.IPMaxQPS > 0 {
		baselineRL = ratelimit.NewRateLimiter(rlCfg.Baseline.GlobalQPS, rlCfg.Baseline.IPMaxQPS)
	}

	// Per-tier middleware (each creates an independent limiter + middleware)
	criticalRL := ratelimit.ForRoutes(rlCfg.Critical.GlobalQPS, rlCfg.Critical.IPMaxQPS)
	checkoutRL := ratelimit.ForRoutes(rlCfg.Checkout.GlobalQPS, rlCfg.Checkout.IPMaxQPS)
	authRL := ratelimit.ForRoutes(rlCfg.Auth.GlobalQPS, rlCfg.Auth.IPMaxQPS)
	standardRL := ratelimit.ForRoutes(rlCfg.Standard.GlobalQPS, rlCfg.Standard.IPMaxQPS)
	adminRL := ratelimit.ForRoutes(rlCfg.Admin.GlobalQPS, rlCfg.Admin.IPMaxQPS)

	log.Printf("[api] tiered rate limiters enabled:")
	log.Printf("[api]   baseline  = global %.0f QPS, per-IP %.0f QPS", rlCfg.Baseline.GlobalQPS, rlCfg.Baseline.IPMaxQPS)
	log.Printf("[api]   critical  = global %.0f QPS, per-IP %.0f QPS", rlCfg.Critical.GlobalQPS, rlCfg.Critical.IPMaxQPS)
	log.Printf("[api]   checkout  = global %.0f QPS, per-IP %.0f QPS", rlCfg.Checkout.GlobalQPS, rlCfg.Checkout.IPMaxQPS)
	log.Printf("[api]   auth      = global %.0f QPS, per-IP %.0f QPS", rlCfg.Auth.GlobalQPS, rlCfg.Auth.IPMaxQPS)
	log.Printf("[api]   standard  = global %.0f QPS, per-IP %.0f QPS", rlCfg.Standard.GlobalQPS, rlCfg.Standard.IPMaxQPS)
	log.Printf("[api]   admin     = global %.0f QPS, per-IP %.0f QPS", rlCfg.Admin.GlobalQPS, rlCfg.Admin.IPMaxQPS)

	h := apiConfig.NewHertzHandler(cfg.Server)

	// Health check endpoint — used by Docker healthcheck and systemd
	h.GET("/healthz", func(c context.Context, ctx *hertzApp.RequestContext) {
		ctx.JSON(200, map[string]string{"status": "ok", "version": version})
	})

	// Baseline rate limiter — loose safety-net applied to ALL endpoints.
	// This is intentionally very permissive; the real protection comes from
	// the per-tier limiters applied at the route level below.
	if baselineRL != nil {
		h.Use(ratelimit.Middleware(baselineRL))
	}

	h.Use(cors.New(cors.Config{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"PUT", "PATCH", "POST", "GET", "DELETE"},
		AllowHeaders: []string{"Origin", "Authorization", "Content-Type"},
	}))

	// Global request timeout middleware — prevents goroutine leaks from
	// slow downstream dependencies (DB, gRPC, external APIs).
	// 15s is generous enough for all normal operations; per-route overrides
	// can be applied below for specific endpoints that need more/less time.
	h.Use(timeout.Middleware(15 * time.Second))

	// Global performance tracking middleware — records every request
	h.Use(perf.Middleware(perfTracker))

	v1 := h.Group("/api/v1")
	{
		// ── Auth tier (strict per-IP: anti brute-force) ────────────────
		v1.POST("/auth/register", authRL, authHandler.Register)
		v1.POST("/auth/login", authRL, authHandler.Login)

		// ── Critical tier + Adaptive Cache (user ordering flow) ────────
		// Only products and groups are browsed by ordering users.
		// Adaptive cache kicks in under high load (QPS >= 200):
		//   criticalRL → catalogCache → handler
		//
		// Products (user browses & selects plans)
		v1.GET("/products", criticalRL, catalogCache, prodHandler.List)
		v1.GET("/products/:id", criticalRL, catalogCache, prodHandler.GetByID)
		// Product lines (frontend instance creation flow — replaces groups)
		v1.GET("/product-lines", criticalRL, catalogCache, productLineHandler.List)

		// ── Critical tier only (no adaptive cache) ─────────────────────
		// These endpoints are admin-facing or low-traffic; rate-limited
		// but no need for adaptive caching.
		v1.GET("/regions", criticalRL, rHandler.ListRegions)
		v1.GET("/regions/:id", criticalRL, rHandler.GetByID)
		v1.GET("/resource-pools", criticalRL, nHandler.ListResourcePools)
		v1.GET("/resource-pools/:id", criticalRL, nHandler.GetResourcePool)
		v1.GET("/locations", criticalRL, nHandler.ListLocations)
		v1.GET("/nodes", criticalRL, nHandler.ListHosts)
		v1.GET("/nodes/:id", criticalRL, nHandler.GetHost)
		v1.GET("/host-nodes", criticalRL, nHandler.ListHosts)
		v1.GET("/host-nodes/:id", criticalRL, nHandler.GetHost)

		// ── No rate limit: Agent endpoints (internal service-to-service) ──
		// Heartbeat / task result loss would cause operational issues.
		v1.POST("/agent/register", nHandler.AgentRegister)
		v1.POST("/agent/heartbeat", nHandler.AgentHeartbeat)
		v1.POST("/agent/tasks/result", nHandler.AgentTaskResult)

		// ── Payment: public endpoints ────────────────────────────────────
		// Payment network list (no auth needed — used by frontend before login)
		v1.GET("/payment/networks", criticalRL, payHandler.Networks)
		// Dynamic payment providers (user-facing — shows enabled providers for checkout)
		v1.GET("/payment/providers", criticalRL, providerHandler.ListEnabled)

		// ── No rate limit: Payment webhook (gateway/blockchain callback) ──
		// Dropping a payment callback could leave orders in limbo.
		v1.POST("/payments/webhook", payHandler.Webhook)
		v1.POST("/payments/webhook/simulate", payHandler.SimulateWebhook)
		// EPay (易支付) webhook — receives callbacks from EPay gateways as GET requests.
		// Supports both V1 (MD5) and V2 (RSA) signature verification.
		// Route: /api/v1/payments/webhook/epay/:providerId
		v1.GET("/payments/webhook/epay/:providerId", payHandler.EPayWebhook)
	}

	// 傳入真實的 jwtService 給中間件
	privateAPI := h.Group("/api/v1")
	privateAPI.Use(middleware.JWTAuthMiddleware(jwtService))
	{
		// ── Standard tier (general authenticated business endpoints) ────
		// User profile
		privateAPI.GET("/me", standardRL, authHandler.Me)
		privateAPI.PUT("/me/password", standardRL, authHandler.ChangePassword)

		// Billing - Invoice routes
		privateAPI.POST("/invoices", standardRL, invoiceHandler.Create)
		privateAPI.GET("/invoices", standardRL, invoiceHandler.ListByCustomer)
		privateAPI.GET("/invoices/:id", standardRL, invoiceHandler.GetByID)
		privateAPI.POST("/invoices/:id/line-items", standardRL, invoiceHandler.AddLineItem)
		privateAPI.PUT("/invoices/:id/tax", standardRL, invoiceHandler.SetTax)
		privateAPI.POST("/invoices/:id/issue", standardRL, invoiceHandler.Issue)
		privateAPI.POST("/invoices/:id/payments", standardRL, invoiceHandler.RecordPayment)
		privateAPI.POST("/invoices/:id/void", standardRL, invoiceHandler.Void)

		// Ordering - Order routes
		privateAPI.POST("/orders", standardRL, orderHandler.Create)
		privateAPI.GET("/orders", standardRL, orderHandler.ListByCustomer)
		privateAPI.GET("/orders/:id", standardRL, orderHandler.GetByID)
		privateAPI.POST("/orders/:id/activate", standardRL, orderHandler.Activate)
		privateAPI.POST("/orders/:id/suspend", standardRL, orderHandler.Suspend)
		privateAPI.POST("/orders/:id/unsuspend", standardRL, orderHandler.Unsuspend)
		privateAPI.POST("/orders/:id/cancel", standardRL, orderHandler.Cancel)
		privateAPI.POST("/orders/:id/terminate", standardRL, orderHandler.Terminate)

		// ── Checkout tier (strict per-IP: purchase/payment writes) ─────
		// Payment — USDT crypto payment flow
		privateAPI.POST("/orders/:id/pay", checkoutRL, payHandler.Pay)
		privateAPI.GET("/payment/charges/:id", standardRL, payHandler.ChargeDetail)

		// Node admin routes (served by node module)
		privateAPI.POST("/nodes", standardRL, nHandler.CreateHost)
		privateAPI.POST("/nodes/:id/enable", standardRL, nHandler.EnableHost)
		privateAPI.POST("/nodes/:id/disable", standardRL, nHandler.DisableHost)

		// Instance - Customer routes
		privateAPI.POST("/instances", standardRL, instHandler.Purchase)
		privateAPI.GET("/instances", standardRL, instHandler.ListByCustomer)
		privateAPI.GET("/instances/:id", standardRL, instHandler.GetByID)
		privateAPI.POST("/instances/:id/start", standardRL, instHandler.Start)
		privateAPI.POST("/instances/:id/stop", standardRL, instHandler.Stop)
		privateAPI.POST("/instances/:id/suspend", standardRL, instHandler.Suspend)
		privateAPI.POST("/instances/:id/unsuspend", standardRL, instHandler.Unsuspend)
		privateAPI.POST("/instances/:id/terminate", standardRL, instHandler.Terminate)
		privateAPI.PUT("/instances/:id/ip", standardRL, instHandler.AssignIP)
		privateAPI.POST("/ws/instances/ticket", standardRL, instanceWSHub.IssueTicket)

		// ── Checkout tier: Product purchase & unified checkout ──────────
		// These involve inventory/funds and need strict per-IP limiting.
		privateAPI.POST("/products/purchase", checkoutRL, prodHandler.Purchase)
		privateAPI.POST("/checkout", checkoutRL, coHandler.Checkout)
		privateAPI.GET("/checkout/orders/:id", standardRL, coHandler.OrderStatus)
		privateAPI.GET("/checkout/orders/:id/stream", coHandler.OrderStatusStream) // SSE — no rate limit (long-lived connection)
		privateAPI.GET("/checkout/stats", standardRL, coHandler.Stats)

		// Product - Admin routes (standard tier)
		privateAPI.POST("/products", standardRL, prodHandler.Create)
		privateAPI.GET("/products/all", standardRL, prodHandler.ListAll)
		privateAPI.POST("/products/:id/enable", standardRL, prodHandler.Enable)
		privateAPI.POST("/products/:id/disable", standardRL, prodHandler.Disable)
		privateAPI.PUT("/products/:id/price", standardRL, prodHandler.UpdatePrice)
		privateAPI.PUT("/products/:id/stock", standardRL, prodHandler.AdjustStock)
		privateAPI.PUT("/products/:id/region", standardRL, prodHandler.SetRegion)

		// Host Node - Admin routes (standard tier)
		privateAPI.POST("/host-nodes", standardRL, nHandler.CreateHost)
		privateAPI.POST("/host-nodes/:id/ips", standardRL, nHandler.AddIP)
		privateAPI.GET("/host-nodes/:id/ips", standardRL, nHandler.ListIPs)
		privateAPI.POST("/host-nodes/:id/tasks", standardRL, nHandler.EnqueueTask)
	}

	// ---- WebSocket routes (auth via ?ticket= one-time token) ----
	h.GET("/api/v1/ws/instances", instanceWSHub.ServeWS)
	h.GET("/api/v1/admin/ws/nodes", wsHub.ServeWS)
	h.GET("/api/v1/admin/ws/performance", perfHub.ServeWS)

	// ---- Admin-only API routes (requires admin role) ----
	// Admin tier rate limiter: RBAC-protected, so limits are relaxed.
	adminAPI := h.Group("/api/v1/admin")
	adminAPI.Use(middleware.AdminMiddleware(jwtService), adminRL)
	{
		// WebSocket ticket endpoint — issues a short-lived one-time ticket
		adminAPI.POST("/ws/ticket", wsHub.IssueTicket)

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
		adminAPI.PUT("/products/:id/stock", prodHandler.AdjustStock)
		adminAPI.PUT("/products/:id/region", prodHandler.SetRegion)

		// Resource Pool management
		adminAPI.POST("/resource-pools", nHandler.CreateResourcePool)
		adminAPI.GET("/resource-pools", nHandler.ListResourcePools)
		adminAPI.GET("/resource-pools/:id", nHandler.GetResourcePool)
		adminAPI.PUT("/resource-pools/:id", nHandler.UpdateResourcePool)
		adminAPI.POST("/resource-pools/:id/activate", nHandler.ActivateResourcePool)
		adminAPI.POST("/resource-pools/:id/deactivate", nHandler.DeactivateResourcePool)
		adminAPI.POST("/resource-pools/:id/nodes", nHandler.AssignNodeToPool)
		adminAPI.DELETE("/resource-pools/:id/nodes/:nodeId", nHandler.RemoveNodeFromPool)

		// Node management (admin)
		adminAPI.POST("/nodes", nHandler.CreateHost)
		adminAPI.POST("/nodes/:id/enable", nHandler.EnableHost)
		adminAPI.POST("/nodes/:id/disable", nHandler.DisableHost)

		// Region management
		adminAPI.POST("/regions", rHandler.Create)
		adminAPI.GET("/regions", rHandler.ListAll)
		adminAPI.GET("/regions/:id", rHandler.GetByID)
		adminAPI.POST("/regions/:id/activate", rHandler.Activate)
		adminAPI.POST("/regions/:id/deactivate", rHandler.Deactivate)

		// Bootstrap token management (for agent registration)
		adminAPI.POST("/bootstrap-tokens", nHandler.CreateBootstrapToken)
		adminAPI.GET("/bootstrap-tokens", nHandler.ListBootstrapTokens)
		adminAPI.DELETE("/bootstrap-tokens/:id", nHandler.RevokeBootstrapToken)

		// Node token revocation (force agent to re-bootstrap)
		adminAPI.POST("/nodes/:id/revoke-token", nHandler.RevokeNodeToken)

		// Unified Checkout — admin endpoints
		adminAPI.PUT("/checkout/threshold", coHandler.SetThreshold)
		adminAPI.GET("/checkout/stats", coHandler.Stats)

		// Performance monitoring — admin endpoints
		adminAPI.POST("/ws/perf-ticket", perfHub.IssueTicket)
		adminAPI.PUT("/performance/interval", perfHub.SetIntervalHandler)
		adminAPI.GET("/performance/snapshot", perfHub.GetSnapshotHandler)

		// Payment Provider management (dynamic provider configuration)
		adminAPI.POST("/payment-providers", providerHandler.Create)
		adminAPI.GET("/payment-providers", providerHandler.ListAll)
		adminAPI.GET("/payment-providers/types", providerHandler.ProviderTypes)
		adminAPI.GET("/payment-providers/:id", providerHandler.GetByID)
		adminAPI.PUT("/payment-providers/:id", providerHandler.Update)
		adminAPI.POST("/payment-providers/:id/enable", providerHandler.Enable)
		adminAPI.POST("/payment-providers/:id/disable", providerHandler.Disable)
		adminAPI.DELETE("/payment-providers/:id", providerHandler.Delete)
	}

	//rootHandler := api.NewRootHandler(cfg.Server)
	//h.GET("/", http.FileServer(http.FS(content)))
	//h.StaticFS("/", hertzApp.FS)
	// Serve embedded frontend only when built with -tags frontend.
	// SPAHandler falls back to index.html for paths that don't match a real
	// static file, enabling Vue Router HTML5 History mode (direct URL access).
	if spaHandler := web.SPAHandler(); spaHandler != nil {
		h.GET("/*filepath", adaptor.HertzHandler(spaHandler))
	}
	// 5. Start gRPC server for agent communication (with node-token auth interceptor)
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(provisioningGrpc.AuthInterceptor(provSvc)))
	agentpb.RegisterAgentServiceServer(grpcServer, provisioningGrpc.NewAgentGRPCServer(provSvc))
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

	// ── Graceful Shutdown ──────────────────────────────────────────────────
	// Listen for SIGINT (Ctrl+C) / SIGTERM (docker stop / k8s preStop).
	// On signal:
	//   1. Stop accepting new HTTP/gRPC connections
	//   2. Drain in-flight requests (up to 10s deadline)
	//   3. Stop background workers (provisioning bus, async checkout, perf hub)
	//   4. Close database connections
	//
	// This prevents request drops during deployments and ensures all
	// in-flight provisioning tasks complete before the process exits.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start HTTP server in background
	go func() {
		h.Spin()
	}()

	sig := <-quit
	log.Printf("[api] received signal %v, starting graceful shutdown...", sig)

	// Create a deadline for the entire shutdown sequence
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	// 1. Stop gRPC server (stops accepting new RPCs, waits for in-flight)
	grpcServer.GracefulStop()
	log.Printf("[api] gRPC server stopped")

	// 2. Stop background workers
	provPollerCancel() // stop provision poller goroutine
	log.Printf("[api] provision poller stopped")
	renewalWorkerCancel()
	log.Printf("[api] renewal worker stopped")

	provisioningBus.Stop()
	log.Printf("[api] provisioning bus stopped")

	// 3. Close database connection pool
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.Close()
		log.Printf("[api] database connections closed")
	}
	if msgClient != nil {
		if err := msgClient.Close(); err != nil {
			log.Printf("[api] failed to close message client: %v", err)
		} else {
			log.Printf("[api] message client closed")
		}
	}

	// Wait for shutdown deadline or completion
	<-shutdownCtx.Done()
	log.Printf("[api] graceful shutdown complete")
}
