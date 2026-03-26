package infra

import (
	"backend-core/internal/catalog/domain"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ------ Mock Repository ------

type mockProductRepo struct {
	getByIDCount      int64
	getBySlugCount    int64
	listAllCount      int64
	listEnabledCount  int64
	listByRegionCount int64
	saveCount         int64

	product  *domain.Product
	products []*domain.Product
	err      error
	delay    time.Duration
}

func newMockProduct(id string) *domain.Product {
	return domain.ReconstituteProduct(
		id, "Test Product", "test-product", "US-east",
		"", "",
		2, 2048, 50, 1000,
		999, "USD", domain.BillingMonthly,
		true, 0, 10, 3,
	)
}

func (m *mockProductRepo) GetByID(ctx context.Context, id string) (*domain.Product, error) {
	atomic.AddInt64(&m.getByIDCount, 1)
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	if m.err != nil {
		return nil, m.err
	}
	if m.product != nil {
		return m.product, nil
	}
	return newMockProduct(id), nil
}

func (m *mockProductRepo) GetBySlug(ctx context.Context, slug string) (*domain.Product, error) {
	atomic.AddInt64(&m.getBySlugCount, 1)
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	if m.err != nil {
		return nil, m.err
	}
	return newMockProduct("slug-" + slug), nil
}

func (m *mockProductRepo) ListAll(ctx context.Context) ([]*domain.Product, error) {
	atomic.AddInt64(&m.listAllCount, 1)
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	if m.err != nil {
		return nil, m.err
	}
	if m.products != nil {
		return m.products, nil
	}
	return []*domain.Product{newMockProduct("p1"), newMockProduct("p2")}, nil
}

func (m *mockProductRepo) ListEnabled(ctx context.Context) ([]*domain.Product, error) {
	atomic.AddInt64(&m.listEnabledCount, 1)
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	if m.err != nil {
		return nil, m.err
	}
	if m.products != nil {
		return m.products, nil
	}
	return []*domain.Product{newMockProduct("p1"), newMockProduct("p2")}, nil
}

func (m *mockProductRepo) ListByRegionID(ctx context.Context, regionID string) ([]*domain.Product, error) {
	atomic.AddInt64(&m.listByRegionCount, 1)
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	if m.err != nil {
		return nil, m.err
	}
	return []*domain.Product{newMockProduct("region-" + regionID)}, nil
}

func (m *mockProductRepo) ConsumeSlotAtomic(ctx context.Context, productID string) error { return nil }

func (m *mockProductRepo) ReleaseSlotAtomic(ctx context.Context, productID string) error { return nil }

func (m *mockProductRepo) Save(ctx context.Context, product *domain.Product) error {
	atomic.AddInt64(&m.saveCount, 1)
	return m.err
}

// ------ Tests ------

func TestSingleflightRepo_GetByID_Deduplicates(t *testing.T) {
	ctx := context.Background()
	mock := &mockProductRepo{delay: 50 * time.Millisecond}
	repo := NewSingleflightProductRepo(mock)

	const concurrency = 50
	var wg sync.WaitGroup
	results := make([]*domain.Product, concurrency)
	errs := make([]error, concurrency)

	barrier := make(chan struct{})

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			<-barrier
			results[idx], errs[idx] = repo.GetByID(ctx, "prod-1")
		}(i)
	}

	close(barrier)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d got error: %v", i, err)
		}
	}

	dbCalls := atomic.LoadInt64(&mock.getByIDCount)
	if dbCalls != 1 {
		t.Fatalf("expected 1 DB call, got %d (singleflight did not deduplicate)", dbCalls)
	}

	for i, p := range results {
		if p.ID() != "prod-1" {
			t.Fatalf("goroutine %d got wrong product ID: %s", i, p.ID())
		}
	}
}

func TestSingleflightRepo_GetByID_DeepCopy(t *testing.T) {
	ctx := context.Background()
	mock := &mockProductRepo{delay: 50 * time.Millisecond}
	repo := NewSingleflightProductRepo(mock)

	const concurrency = 10
	var wg sync.WaitGroup
	results := make([]*domain.Product, concurrency)

	barrier := make(chan struct{})
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			<-barrier
			results[idx], _ = repo.GetByID(ctx, "prod-1")
		}(i)
	}

	close(barrier)
	wg.Wait()

	results[0].Disable()
	for i := 1; i < concurrency; i++ {
		if !results[i].Enabled() {
			t.Fatalf("goroutine %d's product was mutated by goroutine 0 — deep-copy failed", i)
		}
	}
}

func TestSingleflightRepo_ListEnabled_Deduplicates(t *testing.T) {
	ctx := context.Background()
	mock := &mockProductRepo{delay: 50 * time.Millisecond}
	repo := NewSingleflightProductRepo(mock)

	const concurrency = 30
	var wg sync.WaitGroup
	results := make([][]*domain.Product, concurrency)

	barrier := make(chan struct{})
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			<-barrier
			results[idx], _ = repo.ListEnabled(ctx)
		}(i)
	}

	close(barrier)
	wg.Wait()

	dbCalls := atomic.LoadInt64(&mock.listEnabledCount)
	if dbCalls != 1 {
		t.Fatalf("expected 1 DB call for ListEnabled, got %d", dbCalls)
	}

	for i, list := range results {
		if len(list) != 2 {
			t.Fatalf("goroutine %d got %d products, expected 2", i, len(list))
		}
	}
}

func TestSingleflightRepo_ListAll_Deduplicates(t *testing.T) {
	ctx := context.Background()
	mock := &mockProductRepo{delay: 50 * time.Millisecond}
	repo := NewSingleflightProductRepo(mock)

	const concurrency = 20
	var wg sync.WaitGroup

	barrier := make(chan struct{})
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			<-barrier
			repo.ListAll(ctx)
		}()
	}

	close(barrier)
	wg.Wait()

	dbCalls := atomic.LoadInt64(&mock.listAllCount)
	if dbCalls != 1 {
		t.Fatalf("expected 1 DB call for ListAll, got %d", dbCalls)
	}
}

func TestSingleflightRepo_DifferentKeys_NotDeduplicated(t *testing.T) {
	ctx := context.Background()
	mock := &mockProductRepo{}
	repo := NewSingleflightProductRepo(mock)

	repo.GetByID(ctx, "prod-1")
	repo.GetByID(ctx, "prod-2")
	repo.GetByID(ctx, "prod-3")

	dbCalls := atomic.LoadInt64(&mock.getByIDCount)
	if dbCalls != 3 {
		t.Fatalf("expected 3 DB calls for different IDs, got %d", dbCalls)
	}
}

func TestSingleflightRepo_Save_NeverDeduplicated(t *testing.T) {
	ctx := context.Background()
	mock := &mockProductRepo{}
	repo := NewSingleflightProductRepo(mock)

	p := newMockProduct("prod-1")

	repo.Save(ctx, p)
	repo.Save(ctx, p)
	repo.Save(ctx, p)

	if mock.saveCount != 3 {
		t.Fatalf("expected 3 Save calls, got %d (Save should never be deduplicated)", mock.saveCount)
	}
}

func TestSingleflightRepo_ErrorPropagation(t *testing.T) {
	ctx := context.Background()
	expectedErr := errors.New("database connection failed")
	mock := &mockProductRepo{err: expectedErr}
	repo := NewSingleflightProductRepo(mock)

	_, err := repo.GetByID(ctx, "prod-1")
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}

	_, err = repo.ListEnabled(ctx)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
}

func TestSingleflightRepo_GetBySlug_Deduplicates(t *testing.T) {
	ctx := context.Background()
	mock := &mockProductRepo{delay: 50 * time.Millisecond}
	repo := NewSingleflightProductRepo(mock)

	const concurrency = 20
	var wg sync.WaitGroup

	barrier := make(chan struct{})
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			<-barrier
			repo.GetBySlug(ctx, "vps-starter")
		}()
	}

	close(barrier)
	wg.Wait()

	dbCalls := atomic.LoadInt64(&mock.getBySlugCount)
	if dbCalls != 1 {
		t.Fatalf("expected 1 DB call for GetBySlug, got %d", dbCalls)
	}
}

func TestSingleflightRepo_ListByRegionID_Deduplicates(t *testing.T) {
	ctx := context.Background()
	mock := &mockProductRepo{delay: 50 * time.Millisecond}
	repo := NewSingleflightProductRepo(mock)

	const concurrency = 20
	var wg sync.WaitGroup

	barrier := make(chan struct{})
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			<-barrier
			repo.ListByRegionID(ctx, "region-eu")
		}()
	}

	close(barrier)
	wg.Wait()

	dbCalls := atomic.LoadInt64(&mock.listByRegionCount)
	if dbCalls != 1 {
		t.Fatalf("expected 1 DB call for ListByRegionID, got %d", dbCalls)
	}
}

func TestSingleflightRepo_Stats(t *testing.T) {
	mock := &mockProductRepo{}
	repo := NewSingleflightProductRepo(mock)

	stats := repo.Stats()
	if !stats.Enabled {
		t.Fatal("expected singleflight to be enabled")
	}
	if stats.Layer != "repository" {
		t.Fatalf("expected layer 'repository', got %s", stats.Layer)
	}
}

func TestSingleflightRepo_ImplementsInterface(t *testing.T) {
	var _ domain.ProductRepository = (*SingleflightProductRepo)(nil)
}
