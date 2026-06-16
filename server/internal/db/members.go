package db

import (
	"context"
	"strings"

	"github.com/Lakr233/minis/server/internal/model"
)

func (d *DB) UpsertMemberFromAccess(ctx context.Context, accessSub, email string) (*model.Member, error) {
	displayName := email
	if local, _, ok := strings.Cut(email, "@"); ok && local != "" {
		displayName = local
	}

	var member model.Member
	err := d.Pool.QueryRow(ctx, `
		INSERT INTO members (access_sub, email, display_name, role, created_at, updated_at, last_seen_at)
		VALUES ($1, $2, $3, 'member', now(), now(), now())
		ON CONFLICT (access_sub) DO UPDATE SET
			email = EXCLUDED.email,
			updated_at = now(),
			last_seen_at = now()
		RETURNING id, access_sub, email, display_name, role, created_at, updated_at, last_seen_at
	`, accessSub, email, displayName).Scan(
		&member.ID,
		&member.AccessSub,
		&member.Email,
		&member.DisplayName,
		&member.Role,
		&member.CreatedAt,
		&member.UpdatedAt,
		&member.LastSeenAt,
	)
	if err != nil {
		return nil, err
	}
	return &member, nil
}

func (d *DB) GetMemberByAccessSub(ctx context.Context, accessSub string) (*model.Member, error) {
	var member model.Member
	err := d.Pool.QueryRow(ctx, `
		SELECT id, access_sub, email, display_name, role, created_at, updated_at, last_seen_at
		FROM members
		WHERE access_sub = $1
	`, accessSub).Scan(
		&member.ID,
		&member.AccessSub,
		&member.Email,
		&member.DisplayName,
		&member.Role,
		&member.CreatedAt,
		&member.UpdatedAt,
		&member.LastSeenAt,
	)
	if err != nil {
		return nil, err
	}
	return &member, nil
}
