package handlers

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func (h *Handler) isPrivilegedAdminRole(role string) bool {
	switch role {
	case "admin", "setter", "tester":
		return true
	default:
		return false
	}
}

func (h *Handler) userCanAccessAdmin(ctx context.Context, userID int, role string) (bool, error) {
	if h.isPrivilegedAdminRole(role) {
		return true, nil
	}

	var exists bool
	err := h.DB.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1
			FROM app.group_members
			WHERE user_id = $1 AND role = 'admin'
		)`,
		userID,
	).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (h *Handler) managedGroupIDs(ctx context.Context, userID int, role string) ([]int, error) {
	if h.isPrivilegedAdminRole(role) {
		return nil, nil
	}

	rows, err := h.DB.Query(ctx,
		`SELECT group_id
		 FROM app.group_members
		 WHERE user_id = $1 AND role = 'admin'
		 ORDER BY group_id`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := make([]int, 0)
	for rows.Next() {
		var groupID int
		if err := rows.Scan(&groupID); err != nil {
			continue
		}
		groups = append(groups, groupID)
	}

	return groups, nil
}

func (h *Handler) canManageGroup(ctx context.Context, userID int, role string, groupID int) (bool, error) {
	if h.isPrivilegedAdminRole(role) {
		return true, nil
	}

	var exists bool
	err := h.DB.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1
			FROM app.group_members
			WHERE group_id = $1 AND user_id = $2 AND role = 'admin'
		)`,
		groupID, userID,
	).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (h *Handler) canManageContest(ctx context.Context, userID int, role string, contestID int) (bool, error) {
	if h.isPrivilegedAdminRole(role) {
		return true, nil
	}

	var exists bool
	err := h.DB.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1
			FROM app.contests c
			JOIN app.group_members gm ON gm.group_id = c.group_id
			WHERE c.id = $1 AND gm.user_id = $2 AND gm.role = 'admin'
		)`,
		contestID, userID,
	).Scan(&exists)
	if err != nil {
		return false, err
	}

	return exists, nil
}

func (h *Handler) contestGroupID(ctx context.Context, contestID int) (*int, error) {
	var groupID *int
	err := h.DB.QueryRow(ctx,
		`SELECT group_id FROM app.contests WHERE id = $1`,
		contestID,
	).Scan(&groupID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return groupID, nil
}
