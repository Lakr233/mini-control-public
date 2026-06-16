package model

import "time"

type MemberRole string

const (
	MemberRoleMember MemberRole = "member"
	MemberRoleAdmin  MemberRole = "admin"
)

type Member struct {
	ID          string     `json:"id" db:"id"`
	AccessSub   string     `json:"access_sub" db:"access_sub"`
	Email       string     `json:"email" db:"email"`
	DisplayName string     `json:"display_name" db:"display_name"`
	Role        MemberRole `json:"role" db:"role"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at" db:"updated_at"`
	LastSeenAt  time.Time  `json:"last_seen_at" db:"last_seen_at"`
}
