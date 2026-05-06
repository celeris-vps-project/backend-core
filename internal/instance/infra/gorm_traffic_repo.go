package infra

import (
	"time"

	"gorm.io/gorm"
)

// TrafficUsageRecordPO is a detail traffic table which used to calculate traffic and save to TrafficDailyPO
type TrafficUsageRecordPO struct {
	ID         uint64    `gorm:"primaryKey"`
	InstanceID string    `gorm:"not null;index:instance_and_date_idx"`
	RX         uint64    `gorm:"not null"`
	TX         uint64    `gorm:"not null"`
	CreatedAt  time.Time `gorm:"not null;default:CURRENT_TIMESTAMP;index:instance_and_date_idx"`
}

type trafficSumRow struct {
	RX uint64 `gorm:"column:rx"`
	TX uint64 `gorm:"column:tx"`
}

type TrafficCursorPO struct {
	ID         uint64 `gorm:"primaryKey"`
	InstanceID string `gorm:"uniqueIndex;not null"`
	LastRX     uint64 `gorm:"not null"`
	LastTX     uint64 `gorm:"not null"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type TrafficDailyPO struct {
	ID         uint64    `gorm:"primaryKey"`
	InstanceID string    `gorm:"uniqueIndex:idx_traffic_daily_instance_date;not null"`
	Date       time.Time `gorm:"uniqueIndex:idx_traffic_daily_instance_date;not null"`
	RX         uint64    `gorm:"not null"`
	TX         uint64    `gorm:"not null"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type TrafficBillingStatePO struct {
	ID              uint64    `gorm:"primaryKey"`
	InstanceID      string    `gorm:"uniqueIndex;not null"`
	LastEndPeriodAt time.Time `gorm:"not null"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type TrafficRepo struct {
	db *gorm.DB
}

func NewTrafficRepo(db *gorm.DB) *TrafficRepo {
	return &TrafficRepo{
		db: db,
	}
}

func normalizeTrafficBillingAnchor(t time.Time) time.Time {
	t = t.In(time.Local)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
}

func (t *TrafficRepo) FindInstanceTrafficByRange(instanceID string, start, end time.Time) ([]*TrafficUsageRecordPO, error) {
	var traffic []*TrafficUsageRecordPO
	if err := t.db.Model(&TrafficUsageRecordPO{}).
		Where("instance_id = ? and created_at >= ? and created_at < ?", instanceID, start, end).
		Find(&traffic).Error; err != nil {
		return nil, err
	}
	if len(traffic) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return traffic, nil
}

func (t *TrafficRepo) SumUsageTraffic(instanceID string, start, end time.Time) (tx uint64, rx uint64, err error) {
	var row trafficSumRow
	if err := t.db.
		Model(&TrafficUsageRecordPO{}).
		Select("COALESCE(SUM(rx), 0) AS rx, COALESCE(SUM(tx), 0) AS tx").
		Where("instance_id = ? AND created_at >= ? AND created_at < ?", instanceID, start, end).
		Scan(&row).Error; err != nil {
		return 0, 0, err
	}
	return row.TX, row.RX, nil
}

func (t *TrafficRepo) SumDailyTraffic(instanceID string, start, end time.Time) (tx uint64, rx uint64, err error) {
	var row trafficSumRow
	if err := t.db.
		Model(&TrafficDailyPO{}).
		Select("COALESCE(SUM(rx), 0) AS rx, COALESCE(SUM(tx), 0) AS tx").
		Where("instance_id = ? AND date >= ? AND date < ?", instanceID, start, end).
		Scan(&row).Error; err != nil {
		return 0, 0, err
	}
	return row.TX, row.RX, nil
}

func (t *TrafficRepo) ListDailyTrafficByRange(instanceID string, start, end time.Time) ([]*TrafficDailyPO, error) {
	var traffic []*TrafficDailyPO
	if err := t.db.Model(&TrafficDailyPO{}).
		Where("instance_id = ? AND date >= ? AND date < ?", instanceID, start, end).
		Order("date ASC").
		Find(&traffic).Error; err != nil {
		return nil, err
	}
	return traffic, nil
}

func (t *TrafficRepo) SaveTrafficUsageRecord(instanceID string, tx, rx uint64) error {
	return t.db.Model(&TrafficUsageRecordPO{}).Save(&TrafficUsageRecordPO{
		InstanceID: instanceID,
		RX:         rx,
		TX:         tx,
	}).Error
}

func (t *TrafficRepo) GetOrCreateBillingState(instanceID string, anchorAt time.Time) (*TrafficBillingStatePO, error) {
	state := TrafficBillingStatePO{
		InstanceID:      instanceID,
		LastEndPeriodAt: normalizeTrafficBillingAnchor(anchorAt),
	}
	if err := t.db.Where("instance_id = ?", instanceID).FirstOrCreate(&state).Error; err != nil {
		return nil, err
	}
	return &state, nil
}

func (t *TrafficRepo) UpdateBillingState(instanceID string, lastEndPeriodAt time.Time) error {
	return t.db.Model(&TrafficBillingStatePO{}).
		Where("instance_id = ?", instanceID).
		Update("last_end_period_at", normalizeTrafficBillingAnchor(lastEndPeriodAt)).Error
}

func (t *TrafficRepo) FindLatestTrafficCursor(instanceID string) (*TrafficCursorPO, error) {
	var traffic TrafficCursorPO
	if err := t.db.Model(&TrafficCursorPO{}).Where("instance_id = ?", instanceID).First(&traffic).Error; err != nil {
		return nil, err
	}
	return &traffic, nil
}

func (t *TrafficRepo) UpdateCursor(instanceID string, lastTX, lastRX uint64) error {
	cursor := TrafficCursorPO{
		InstanceID: instanceID,
		LastRX:     lastRX,
		LastTX:     lastTX,
	}
	return t.db.Where("instance_id = ?", instanceID).Assign(TrafficCursorPO{LastRX: lastRX, LastTX: lastTX}).FirstOrCreate(&cursor).Error
}

func (t *TrafficRepo) FindLatestTrafficDaily(instanceID string) (*TrafficDailyPO, error) {
	var traffic TrafficDailyPO
	now := time.Now().In(time.Local)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local) // 今天 0 点
	lastDay := today.AddDate(0, 0, -1)
	if err := t.db.Model(&TrafficDailyPO{}).Where("instance_id = ? and date = ?", instanceID, lastDay).First(&traffic).Error; err != nil {
		return nil, err
	}
	return &traffic, nil
}

func (t *TrafficRepo) SaveDailyTraffic(instanceID string, tx, rx uint64) error {
	now := time.Now().In(time.Local)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local) // 今天 0 点
	lastDay := today.AddDate(0, 0, -1)
	dailyPO := TrafficDailyPO{
		InstanceID: instanceID,
		Date:       lastDay,
		RX:         rx,
		TX:         tx,
	}
	return t.db.Model(&TrafficDailyPO{}).Create(&dailyPO).Error
}
