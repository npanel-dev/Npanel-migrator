package xiaov2board

import (
	"context"
	"database/sql"
	"fmt"

	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/db"
)

// ExtractCoupons 从 v2_coupon 读取优惠券。
// 字段映射：v2_coupon.type(1百分比2固定)/value → NPanel type/discount；
// started_at/ended_at(Unix秒) → start_time/expire_time(毫秒，×1000)。
func ExtractCoupons(ctx context.Context, cfg db.Config) ([]*canonical.Coupon, error) {
	var coupons []*canonical.Coupon
	err := db.QueryRows(ctx, cfg,
		"SELECT id, code, name, type, value, `show`, limit_use, started_at, ended_at, created_at, updated_at "+
			"FROM v2_coupon ORDER BY id",
		func(rows *sql.Rows) error {
			c, err := scanCoupon(rows)
			if err != nil {
				return err
			}
			coupons = append(coupons, c)
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取 v2_coupon 失败: %w", err)
	}
	return coupons, nil
}

func scanCoupon(rows *sql.Rows) (*canonical.Coupon, error) {
	var (
		id, limitUse, startedAt, endedAt, createdAt, updatedAt sql.NullInt64
		code, name                                             sql.NullString
		cType                                                  sql.NullInt64
		value                                                  sql.NullInt64
		show                                                   sql.NullInt64
	)
	if err := rows.Scan(&id, &code, &name, &cType, &value, &show, &limitUse,
		&startedAt, &endedAt, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	return &canonical.Coupon{
		SourceID:   id.Int64,
		Name:       name.String,
		Code:       code.String,
		Type:       int8(cType.Int64),
		Discount:   value.Int64,
		Count:      0,
		UserLimit:  limitUse.Int64,
		UsedCount:  0, // v2board 无此字段
		StartTime:  startedAt.Int64 * 1000,
		ExpireTime: endedAt.Int64 * 1000,
		Enable:     show.Int64 == 1,
		CreatedAt:  unixToTime(createdAt.Int64),
		UpdatedAt:  unixToTime(updatedAt.Int64),
	}, nil
}

// ExtractNotices 从 v2_notice 读取公告。
// 字段映射：v2_notice.title/content/show → NPanel announcement title/content/show。
func ExtractNotices(ctx context.Context, cfg db.Config) ([]*canonical.Notice, error) {
	var notices []*canonical.Notice
	err := db.QueryRows(ctx, cfg,
		"SELECT id, title, content, `show`, created_at, updated_at FROM v2_notice ORDER BY id",
		func(rows *sql.Rows) error {
			var id, show, createdAt, updatedAt sql.NullInt64
			var title, content sql.NullString
			if err := rows.Scan(&id, &title, &content, &show, &createdAt, &updatedAt); err != nil {
				return err
			}
			notices = append(notices, &canonical.Notice{
				SourceID:  id.Int64,
				Title:     title.String,
				Content:   content.String,
				Show:      show.Int64 == 1,
				CreatedAt: unixToTime(createdAt.Int64),
				UpdatedAt: unixToTime(updatedAt.Int64),
			})
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取 v2_notice 失败: %w", err)
	}
	return notices, nil
}

// ExtractTickets 从 v2_ticket 读取工单（含 v2_ticket_message 消息）。
// 字段映射：v2_ticket.subject → NPanel ticket.title；status 映射 NPanel 工单状态。
func ExtractTickets(ctx context.Context, cfg db.Config) ([]*canonical.Ticket, error) {
	var tickets []*canonical.Ticket
	err := db.QueryRows(ctx, cfg,
		"SELECT id, user_id, subject, status, reply_status, created_at, updated_at FROM v2_ticket ORDER BY id",
		func(rows *sql.Rows) error {
			var (
				id, userID, status, replyStatus, createdAt, updatedAt sql.NullInt64
				subject                                               sql.NullString
			)
			if err := rows.Scan(&id, &userID, &subject, &status, &replyStatus, &createdAt, &updatedAt); err != nil {
				return err
			}
			tickets = append(tickets, &canonical.Ticket{
				SourceID:     id.Int64,
				Title:        subject.String,
				UserSourceID: userID.Int64,
				Status:       mapTicketStatus(int8(status.Int64), int8(replyStatus.Int64)),
				CreatedAt:    unixToTime(createdAt.Int64),
				UpdatedAt:    unixToTime(updatedAt.Int64),
			})
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取 v2_ticket 失败: %w", err)
	}

	// 读取工单消息并按 ticket_id 分组。
	follows, err := extractTicketMessages(ctx, cfg)
	if err != nil {
		return nil, err
	}
	for _, t := range tickets {
		for _, f := range follows {
			if f.TicketSourceID == t.SourceID {
				t.Follows = append(t.Follows, *f)
			}
		}
	}
	return tickets, nil
}

// extractTicketMessages 读取 v2_ticket_message。
// 映射：user_id=工单创建者 → from="user"；user_id=1(管理员) → from="admin"。
func extractTicketMessages(ctx context.Context, cfg db.Config) ([]*canonical.TicketFollow, error) {
	var follows []*canonical.TicketFollow
	err := db.QueryRows(ctx, cfg,
		"SELECT id, user_id, ticket_id, message, created_at FROM v2_ticket_message ORDER BY id",
		func(rows *sql.Rows) error {
			var id, userID, ticketID, createdAt sql.NullInt64
			var message sql.NullString
			if err := rows.Scan(&id, &userID, &ticketID, &message, &createdAt); err != nil {
				return err
			}
			from := "user"
			if userID.Int64 == 1 { // 管理员回复
				from = "admin"
			}
			follows = append(follows, &canonical.TicketFollow{
				SourceID:       id.Int64,
				TicketSourceID: ticketID.Int64,
				UserSourceID:   userID.Int64,
				Content:        message.String,
				Type:           1, // text
				From:           from,
				CreatedAt:      unixToTime(createdAt.Int64),
			})
			return nil
		},
	)
	return follows, err
}

// mapTicketStatus v2_ticket.status/reply_status → NPanel ticket.status。
// NPanel: 1=Pending 2=Waiting 3=Processed 4=Closed
// v2board: status 0=已开启 1=已回复; reply_status 0=等待回复 1=已回复
func mapTicketStatus(status, replyStatus int8) int8 {
	if status == 1 && replyStatus == 1 {
		return 3 // 已处理
	}
	if replyStatus == 0 {
		return 2 // 等待用户回复
	}
	return 1 // 待处理
}
