package app

import (
	"backend-core/internal/instance/domain"
	"backend-core/internal/instance/infra"
	"backend-core/pkg/eventbus"
	"backend-core/pkg/events"
	"context"
	"errors"
	"log"
	"time"

	"gorm.io/gorm"
)

type TrafficService struct {
	bus          *eventbus.EventBus
	trafficRepo  *infra.TrafficRepo
	instanceRepo *infra.GormInstanceRepo
	instanceSvc  *InstanceAppService
}

func NewTrafficService(bus *eventbus.EventBus, trafficRepo *infra.TrafficRepo, instanceRepo *infra.GormInstanceRepo, instanceSvc *InstanceAppService) *TrafficService {
	trafficService := &TrafficService{
		bus:          bus,
		trafficRepo:  trafficRepo,
		instanceRepo: instanceRepo,
		instanceSvc:  instanceSvc,
	}

	if trafficService.bus != nil {
		trafficService.bus.Subscribe(events.InstanceTrafficRecordUpdatedEvent{}.EventName(), trafficService.EventHandler)
	}
	return trafficService
}

func (t *TrafficService) EventHandler(event eventbus.Event) {

	evt, ok := event.(events.InstanceTrafficRecordUpdatedEvent)
	if !ok {
		// 这里不用这样写也可以，因为一个事件只有一个handler
		return
	}
	// 必须定位一下判断上次活跃信息，如果上次宕机了，就不能直接记录了
	// 先算没关机的情况

	cursor, err := t.trafficRepo.FindLatestTrafficCursor(evt.InstanceID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		cursor = &infra.TrafficCursorPO{}
	} else if err != nil {
		log.Printf("[instance.traffic] Error finding latest traffic cursor: %v\n", err)
		return
	}
	lastTime, err := time.Parse(time.RFC3339, evt.Date)
	if err != nil {
		// 不会发生的
		log.Println(err)
		return
	}
	// 更新过了或者没有流量产生
	if time.Now().Before(lastTime) || (cursor.LastRX == evt.TotalRX && cursor.LastTX == evt.TotalTX) {
		return
	}

	if cursor.LastRX <= evt.TotalRX && cursor.LastTX <= evt.TotalTX {
		// 这种情况没有关机，直接插入记录就行
		if err := t.trafficRepo.SaveTrafficUsageRecord(evt.InstanceID, evt.TotalTX-cursor.LastTX, evt.TotalRX-cursor.LastRX); err != nil {
			log.Printf("[instance.traffic] Error updating traffic usage: %v", err)
			return
		}

		if err := t.trafficRepo.UpdateCursor(evt.InstanceID, evt.TotalTX, evt.TotalRX); err != nil {
			log.Printf("Error updating traffic usage cursor: %v", err)
			return
		}

		return
	}

	// 这里是关机的情况，cursor.LastTX > evt.TotalTX, 这时候evt.TotalTX 就是上一次累积的量，但必须比较是否是第一次累加，不然会错，如果是第一次累加，则这次流量cursor必须置为0，后面就不用计算了，会正常走上面流程
	// 关键必须先更新这里
	if err := t.trafficRepo.UpdateCursor(evt.InstanceID, evt.TotalTX, evt.TotalRX); err != nil {
		log.Printf("Error updating traffic usage cursor: %v", err)
		return
	}

	if err := t.trafficRepo.SaveTrafficUsageRecord(evt.InstanceID, evt.TotalTX, evt.TotalRX); err != nil {
		log.Printf("Error updating traffic usage: %v", err)
		return
	}

}

func (t *TrafficService) StartCalculateDailyTraffic(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Hour * 5)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := t.CalculateDailyTraffic(); err != nil {
					log.Printf("[traffic] calculate daily traffic failed: %v", err)
				}
			}
		}
	}()
}

func yesterdayWindow(now time.Time, loc *time.Location) (start, end time.Time) {
	now = now.In(loc)
	end = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc) // 今天 0 点
	start = end.AddDate(0, 0, -1)                                        // 昨天 0 点
	return
}

func normalizeTrafficPeriodTime(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

func (t *TrafficService) GetInstanceTrafficUsage(instanceID string) (*domain.TrafficUsageSummary, error) {
	inst, err := t.instanceRepo.GetByID(instanceID)
	if err != nil {
		return nil, err
	}
	state, err := t.trafficRepo.GetOrCreateBillingState(instanceID, inst.CreatedAt())
	if err != nil {
		return nil, err
	}
	start := normalizeTrafficPeriodTime(state.LastEndPeriodAt, time.Local)
	today := normalizeTrafficPeriodTime(time.Now(), time.Local)
	end := today.AddDate(0, 0, 1)
	if end.Before(start) {
		end = start
	}
	dailyEnd := today
	if dailyEnd.Before(start) {
		dailyEnd = start
	}
	tx, rx, err := t.trafficRepo.SumDailyTraffic(instanceID, start, dailyEnd)
	if err != nil {
		return nil, err
	}
	dailyRows, err := t.trafficRepo.ListDailyTrafficByRange(instanceID, start, dailyEnd)
	if err != nil {
		return nil, err
	}
	rawStart := today
	if rawStart.Before(start) {
		rawStart = start
	}
	//rawTX, rawRX, err := t.trafficRepo.SumUsageTraffic(instanceID, rawStart, end)
	//if err != nil {
	//	return nil, err
	//}
	//tx += rawTX
	//rx += rawRX
	daily := make([]domain.TrafficDaily, 0, len(dailyRows))
	for _, row := range dailyRows {
		if row == nil {
			continue
		}
		daily = append(daily, domain.TrafficDaily{
			ID:         row.ID,
			InstanceID: row.InstanceID,
			Date:       row.Date,
			RX:         row.RX,
			TX:         row.TX,
			CreateAt:   row.CreatedAt,
		})
	}
	//if rawTX > 0 || rawRX > 0 {
	//	daily = append(daily, domain.TrafficDaily{
	//		InstanceID: instanceID,
	//		Date:       rawStart,
	//		RX:         rawRX,
	//		TX:         rawTX,
	//		CreateAt:   time.Now(),
	//	})
	//}
	total := rx + tx
	periodMax := bandwidthGBToBytes(inst.BandwidthGB())
	overLimit := periodMax > 0 && total > periodMax
	if overLimit {
		t.suspendTrafficRunOut(inst)
	}
	return &domain.TrafficUsageSummary{
		InstanceID:      instanceID,
		LastEndPeriodAt: start,
		PeriodStart:     start,
		PeriodEnd:       end,
		RX:              rx,
		TX:              tx,
		Total:           total,
		BandwidthGB:     inst.BandwidthGB(),
		PeriodMax:       periodMax,
		OverLimit:       overLimit,
		Daily:           daily,
	}, nil
}

func bandwidthGBToBytes(gb int) uint64 {
	if gb <= 0 {
		return 0
	}
	return uint64(gb) * 1024 * 1024 * 1024
}

func (t *TrafficService) suspendTrafficRunOut(inst *domain.Instance) {
	if t.instanceSvc == nil || inst == nil {
		return
	}
	switch inst.ControlStatus() {
	case domain.InstanceControlStatusActive:
	default:
		return
	}
	if err := t.instanceSvc.SuspendInstanceWithReason(inst.ID(), domain.InstanceSuspendReasonTrafficRunOut); err != nil {
		log.Printf("[instance.traffic] suspend instance %s for traffic run out failed: %v", inst.ID(), err)
	}
}

func (t *TrafficService) MarkTrafficPeriodBilled(instanceID string, periodEnd time.Time) error {
	return t.trafficRepo.UpdateBillingState(instanceID, periodEnd)
}

func (t *TrafficService) CalculateDailyTraffic() error {
	// 应该出账昨天的, 先找有没有这条记录
	start, end := yesterdayWindow(time.Now(), time.Local)
	instances, err := t.instanceRepo.ListAll()
	if err != nil {
		return err
	}
	for _, instance := range instances {
		lastDayTraffic, err := t.trafficRepo.FindLatestTrafficDaily(instance.ID())
		if err == nil && lastDayTraffic != nil {
			continue
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("[instance.traffic] Error finding latest daily traffic: %v", err)
			continue
		}
		// 找不到再插入
		tx, rx, err := t.trafficRepo.SumUsageTraffic(instance.ID(), start, end)
		if err != nil {
			log.Printf("[instance.traffic] Error saving daily traffic usage record: %v", err)
			continue
		}
		if err := t.trafficRepo.SaveDailyTraffic(instance.ID(), tx, rx); err != nil {
			log.Printf("[instance.traffic] Error saving daily traffic usage record: %v", err)
			continue
		}
	}

	return nil
}

func (t *TrafficService) GCTrafficUsageRecordDaily() {

}
