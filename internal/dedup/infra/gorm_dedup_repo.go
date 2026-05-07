package infra

import "time"

type DedupPO struct {
	ID        string
	BizKey    string
	DedupKey  string
	BizType   string
	CreatedAt time.Time
}
