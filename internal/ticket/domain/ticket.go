package domain

type Ticket struct {
	ID             string `json:"id"`
	UserID         string `json:"user_id"`          // 用户发起的ticket
	TicketDetailID string `json:"ticket_detail_id"` // 对应ticket的对话记录
}
