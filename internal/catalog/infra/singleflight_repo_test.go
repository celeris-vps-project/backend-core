package infra

import (
	"backend-core/internal/catalog/domain"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ------ Mock Repository ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------

type mockProductRepo struct {
	getByIDCount      int64
	getBySlugCount    int64
	listAllCount      int64
	listEnabledCount  int64
	listByRegionCount int64
	saveCount         int64

	// Configurable return values
	product  *domain.Product
	products []*domain.Product
	err      error

	// delay simulates DB query latency so singleflight's in-flight window
	// overlaps across concurrent goroutines in tests.
	delay time.Duration
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

func (m *mockProductRepo) GetByID(id string) (*domain.Product, error) {
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

func (m *mockProductRepo) GetBySlug(slug string) (*domain.Product, error) {
	atomic.AddInt64(&m.getBySlugCount, 1)
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	if m.err != nil {
		return nil, m.err
	}
	return newMockProduct("slug-" + slug), nil
}

func (m *mockProductRepo) ListAll() ([]*domain.Product, error) {
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

func (m *mockProductRepo) ListEnabled() ([]*domain.Product, error) {
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

func (m *mockProductRepo) ListByRegionID(regionID string) ([]*domain.Product, error) {
	atomic.AddInt64(&m.listByRegionCount, 1)
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	if m.err != nil {
		return nil, m.err
	}
	return []*domain.Product{newMockProduct("region-" + regionID)}, nil
}

func (m *mockProductRepo) ConsumeSlotAtomic(productID string) error { return nil }

func (m *mockProductRepo) ReleaseSlotAtomic(productID string) error { return nil }

func (m *mockProductRepo) Save(product *domain.Product) error {
	atomic.AddInt64(&m.saveCount, 1)
	return m.err
}

// ------ Tests ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------

func TestSingleflightRepo_GetByID_Deduplicates(t *testing.T) {
	mock := &mockProductRepo{delay: 50 * time.Millisecond}
	repo := NewSingleflightProductRepo(mock)

	const concurrency = 50
	var wg sync.WaitGroup
	results := make([]*domain.Product, concurrency)
	errs := make([]error, concurrency)

	// Use a barrier to ensure all goroutines start at the same time
	barrier := make(chan struct{})

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			<-barrier // wait for all goroutines to be ready
			results[idx], errs[idx] = repo.GetByID("prod-1")
		}(i)
	}

	close(barrier) // release all goroutines simultaneously
	wg.Wait()

	// All calls should succeed
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d got error: %v", i, err)
		}
	}

	// The inner repo should have been called only ONCE (singleflight dedup)
	dbCalls := atomic.LoadInt64(&mock.getByIDCount)
	if dbCalls != 1 {
		t.Fatalf("expected 1 DB call, got %d (singleflight did not deduplicate)", dbCalls)
	}

	// All results should have the same ID
	for i, p := range results {
		if p.ID() != "prod-1" {
			t.Fatalf("goroutine %d got wrong product ID: %s", i, p.ID())
		}
	}
}

func TestSingleflightRepo_GetByID_DeepCopy(t *testing.T) {
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
			results[idx], _ = repo.GetByID("prod-1")
		}(i)
	}

	close(barrier)
	wg.Wait()

	// Mutate one result --?should NOT affect others (deep-copy safety)
	results[0].Disable()
	for i := 1; i < concurrency; i++ {
		if !results[i].Enabled() {
			t.Fatalf("goroutine %d's product was mutated by goroutine 0 --?deep-copy failed", i)
		}
	}
}

func TestSingleflightRepo_ListEnabled_Deduplicates(t *testing.T) {
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
			results[idx], _ = repo.ListEnabled()
		}(i)
	}

	close(barrier)
	wg.Wait()

	dbCalls := atomic.LoadInt64(&mock.listEnabledCount)
	if dbCalls != 1 {
		t.Fatalf("expected 1 DB call for ListEnabled, got %d", dbCalls)
	}

	// All should return 2 products
	for i, list := range results {
		if len(list) != 2 {
			t.Fatalf("goroutine %d got %d products, expected 2", i, len(list))
		}
	}
}

func TestSingleflightRepo_ListAll_Deduplicates(t *testing.T) {
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
			repo.ListAll()
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
	mock := &mockProductRepo{}
	repo := NewSingleflightProductRepo(mock)

	// Sequential calls with different IDs --?each should hit the DB
	repo.GetByID("prod-1")
	repo.GetByID("prod-2")
	repo.GetByID("prod-3")

	dbCalls := atomic.LoadInt64(&mock.getByIDCount)
	if dbCalls != 3 {
		t.Fatalf("expected 3 DB calls for different IDs, got %d", dbCalls)
	}
}

func TestSingleflightRepo_Save_NeverDeduplicated(t *testing.T) {
	mock := &mockProductRepo{}
	repo := NewSingleflightProductRepo(mock)

	p := newMockProduct("prod-1")

	// Save should always pass through
	repo.Save(p)
	repo.Save(p)
	repo.Save(p)

	if mock.saveCount != 3 {
		t.Fatalf("expected 3 Save calls, got %d (Save should never be deduplicated)", mock.saveCount)
	}
}

func TestSingleflightRepo_ErrorPropagation(t *testing.T) {
	expectedErr := errors.New("database connection failed")
	mock := &mockProductRepo{err: expectedErr}
	repo := NewSingleflightProductRepo(mock)

	_, err := repo.GetByID("prod-1")
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}

	_, err = repo.ListEnabled()
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
}

func TestSingleflightRepo_GetBySlug_Deduplicates(t *testing.T) {
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
			repo.GetBySlug("vps-starter")
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
			repo.ListByRegionID("region-eu")
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
	// Compile-time check is in the source file; this just verifies at test time.
	var _ domain.ProductRepository = (*SingleflightProductRepo)(nil)
}
