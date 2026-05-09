package infra

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

const trafficPartitionLockKey int64 = 202605090001

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

func (t *TrafficRepo) CleanUPTrafficUsage(instanceID string) (*TrafficDailyPO, error) {
	var traffic TrafficDailyPO
	now := time.Now().In(time.Local)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local) // 今天 0 点
	lastDayBefore := today.AddDate(0, 0, -2)
	if err := t.db.Model(&TrafficDailyPO{}).Where("instance_id = ? and created_at < ?", instanceID, lastDayBefore).First(&traffic).Error; err != nil {
		return nil, err
	}
	return &traffic, nil
}

func (t *TrafficRepo) EnsureTrafficUsageRecordPosPartitions() error {
	return t.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(2026050901)`).Error; err != nil {
			return err
		}
		sql := `
DO $$
DECLARE
    d date;
BEGIN
    d := current_date;
    WHILE d <= current_date + 30 LOOP
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF public.traffic_usage_record_pos
             FOR VALUES FROM (%L) TO (%L)',
            'traffic_usage_record_pos_' || to_char(d, 'YYYYMMDD'),
            d,
            d + 1
        );
        d := d + 1;
    END LOOP;
END $$;
`
		return tx.Exec(sql).Error
	})
}

func maintainTrafficUsageRecordPosPartitions(db *gorm.DB, futureDays int, retentionDays int) error {
	return db.Transaction(func(tx *gorm.DB) error {

		// 多实例部署时，保证同一时间只有一个实例在做分区维护
		var ok bool
		if err := tx.Raw(
			`WITH _ AS (SELECT pg_advisory_xact_lock(?)) SELECT true`,
			trafficPartitionLockKey,
		).Scan(&ok).Error; err != nil {
			return err
		}
		// 你的 created_at 是 timestamptz，建议固定用 UTC 建分区边界
		if err := tx.Exec(`SET LOCAL TIME ZONE 'UTC'`).Error; err != nil {
			return err
		}
		// 创建今天到未来 30 天分区
		if err := ensureTrafficUsageRecordPosPartitionsTx(tx, futureDays); err != nil {
			return err
		}
		// 删除 30 天以前的旧分区
		if err := dropOldTrafficUsageRecordPosPartitionsTx(tx, retentionDays); err != nil {
			return err
		}
		return nil
	})
}

func ensureTrafficUsageRecordPosPartitionsTx(tx *gorm.DB, daysAhead int) error {
	if daysAhead < 0 || daysAhead > 3660 {
		return fmt.Errorf("invalid daysAhead: %d", daysAhead)
	}
	sql := fmt.Sprintf(`
DO $$
DECLARE
    d date;
BEGIN
    d := current_date;
    WHILE d <= current_date + %d LOOP
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %%I.%%I PARTITION OF %%I.%%I
             FOR VALUES FROM (%%L) TO (%%L)',
            'public',
            'traffic_usage_record_pos_' || to_char(d, 'YYYYMMDD'),
            'public',
            'traffic_usage_record_pos',
            d,
            d + 1
        );
        d := d + 1;
    END LOOP;
END $$;
`, daysAhead)
	return tx.Exec(sql).Error
}

func dropOldTrafficUsageRecordPosPartitionsTx(tx *gorm.DB, retentionDays int) error {
	if retentionDays <= 0 || retentionDays > 3660 {
		return fmt.Errorf("invalid retentionDays: %d", retentionDays)
	}

	sql := fmt.Sprintf(`
DO $$
DECLARE
    r record;
    cutoff date;
BEGIN
    cutoff := current_date - %d;

    FOR r IN
        SELECT
            n.nspname AS schemaname,
            c.relname AS relname,
            to_date(substring(c.relname from '([0-9]{8})$'), 'YYYYMMDD') AS pdate
        FROM pg_inherits i
        JOIN pg_class c ON c.oid = i.inhrelid
        JOIN pg_namespace n ON n.oid = c.relnamespace
        JOIN pg_class p ON p.oid = i.inhparent
        JOIN pg_namespace pn ON pn.oid = p.relnamespace
        WHERE pn.nspname = 'public'
          AND p.relname = 'traffic_usage_record_pos'
          AND n.nspname = 'public'
          AND c.relname ~ '^traffic_usage_record_pos_[0-9]{8}$'
          AND to_date(substring(c.relname from '([0-9]{8})$'), 'YYYYMMDD') < cutoff
        ORDER BY pdate
    LOOP
        EXECUTE format('DROP TABLE IF EXISTS %%I.%%I', r.schemaname, r.relname);
    END LOOP;
END $$;
`, retentionDays)

	return tx.Exec(sql).Error
}

func (t *TrafficRepo) StartTrafficPartitionCron(ctx context.Context) (*cron.Cron, error) {

	// 程序启动时先跑一次，避免刚启动就没有未来分区
	db := t.db
	if err := maintainTrafficUsageRecordPosPartitions(db, 30, 30); err != nil {
		return nil, err
	}
	c := cron.New(
		cron.WithLocation(time.Local),
		cron.WithChain(
			cron.Recover(cron.DefaultLogger),
			cron.SkipIfStillRunning(cron.DefaultLogger),
		),
	)
	// 每天 UTC 00:10 跑一次
	// robfig/cron v3 默认是 5 段表达式：分 时 日 月 周
	_, err := c.AddFunc("10 0 * * *", func() {
		if err := maintainTrafficUsageRecordPosPartitions(db, 30, 30); err != nil {
			log.Printf("[traffic partition] maintenance failed: %v", err)
		}
	})
	if err != nil {
		return nil, err
	}
	c.Start()
	go func() {
		<-ctx.Done()
		stopCtx := c.Stop()
		<-stopCtx.Done()
	}()
	return c, nil
}
