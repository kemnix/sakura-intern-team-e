package model

import "time"

type Notification struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`
	Actor     User      `json:"actor"`
	PostID    *int64    `json:"post_id,omitempty"`
	IsRead    bool      `json:"is_read"`
	CreatedAt time.Time `json:"created_at"`
}

type Footprint struct {
	Visitor     User      `json:"visitor"`
	VisitCount  int       `json:"visit_count"`
	LastVisited time.Time `json:"last_visited"`
}
