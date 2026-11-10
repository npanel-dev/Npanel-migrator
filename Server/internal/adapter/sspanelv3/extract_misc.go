package sspanelv3

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/db"
)

// couponUnlimitedCount 源面板优惠券无总量字段，用大数表示不限量。
const couponUnlimitedCount = 999999

// ExtractCoupons 读取 coupon → canonical.Coupon。
// 字段映射（源码 UserController 核实）：credit=减免百分比（total=price×(100-credit)/100），
// expire=Unix 秒，onetime=每用户限用一次，shop=适用商品 id 串（源 id 与目标不同，置空=全部适用）。
func ExtractCoupons(ctx context.Context, cfg db.Config) ([]*canonical.Coupon, error) {
	var coupons []*canonical.Coupon
	err := db.QueryRows(ctx, cfg,
		"SELECT id, code, onetime, expire, shop, credit FROM coupon ORDER BY id",
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
		return nil, fmt.Errorf("读取 coupon 失败: %w", err)
	}
	return coupons, nil
}

func scanCoupon(rows *sql.Rows) (*canonical.Coupon, error) {
	var (
		id      int64
		code    sql.NullString
		onetime sql.NullInt64
		expire  sql.NullInt64
		shop    sql.NullString
		credit  sql.NullInt64
	)
	if err := rows.Scan(&id, &code, &onetime, &expire, &shop, &credit); err != nil {
		return nil, err
	}
	var userLimit int64
	if onetime.Int64 == 1 {
		userLimit = 1
	}
	created := time.Time{}
	if expire.Int64 > 0 {
		created = unixToTime(expire.Int64)
	}
	return &canonical.Coupon{
		SourceID:   id,
		Name:       fmt.Sprintf("SSPanel 优惠券 #%d", id),
		Code:       strings.TrimSpace(code.String),
		Type:       1, // 1=百分比折扣
		Discount:   credit.Int64,
		Count:      couponUnlimitedCount,
		UserLimit:  userLimit,
		UsedCount:  0,
		StartTime:  0,
		ExpireTime: expire.Int64 * 1000,
		Enable:     expire.Int64 == 0 || expire.Int64 > time.Now().Unix(),
		// shop 字段是源商品 id 串，与目标套餐 id 无对应关系，置空表示全部适用。
		Subscribe: "",
		CreatedAt: orEpoch(created),
		UpdatedAt: orEpoch(created),
	}, nil
}

// ExtractNotices 读取 announcement → canonical.Notice。
// 源表无标题字段，生成 "SSPanel 公告 #id"；正文 markdown 优先。
func ExtractNotices(ctx context.Context, cfg db.Config) ([]*canonical.Notice, error) {
	var notices []*canonical.Notice
	err := db.QueryRows(ctx, cfg,
		"SELECT id, date, content, markdown FROM announcement ORDER BY id",
		func(rows *sql.Rows) error {
			n, err := scanNotice(rows)
			if err != nil {
				return err
			}
			notices = append(notices, n)
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取 announcement 失败: %w", err)
	}
	return notices, nil
}

func scanNotice(rows *sql.Rows) (*canonical.Notice, error) {
	var (
		id       int64
		date     sql.NullString
		content  sql.NullString
		markdown sql.NullString
	)
	if err := rows.Scan(&id, &date, &content, &markdown); err != nil {
		return nil, err
	}
	body := strings.TrimSpace(markdown.String)
	if body == "" {
		body = strings.TrimSpace(content.String)
	}
	createdAt := parseDateTime(date.String)
	return &canonical.Notice{
		SourceID:  id,
		Title:     fmt.Sprintf("SSPanel 公告 #%d", id),
		Content:   body,
		Show:      true,
		Pinned:    false,
		CreatedAt: orEpoch(createdAt),
		UpdatedAt: orEpoch(createdAt),
	}, nil
}

// ExtractTickets 读取 ticket → canonical.Ticket（rootid>0 的行归并为主单的 Follows）。
// 状态：1=开启 → NPanel 1(Pending)，其他 → 4(Closed)。
func ExtractTickets(ctx context.Context, cfg db.Config) ([]*canonical.Ticket, error) {
	// 先读主单（rootid=0）。
	tickets := make(map[int64]*canonical.Ticket)
	var order []int64
	err := db.QueryRows(ctx, cfg,
		"SELECT id, title, content, userid, datetime, status FROM ticket WHERE rootid = 0 ORDER BY id",
		func(rows *sql.Rows) error {
			t, err := scanTicket(rows)
			if err != nil {
				return err
			}
			tickets[t.SourceID] = t
			order = append(order, t.SourceID)
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取 ticket 失败: %w", err)
	}
	// 再读回复（rootid>0），按 rootid 归并。
	err = db.QueryRows(ctx, cfg,
		"SELECT id, title, content, rootid, userid, datetime, status FROM ticket WHERE rootid > 0 ORDER BY id",
		func(rows *sql.Rows) error {
			f, rootID, err := scanTicketFollow(rows)
			if err != nil {
				return err
			}
			parent, ok := tickets[rootID]
			if !ok {
				return nil // 悬空回复（主单已删），跳过
			}
			f.From = ticketFollowFrom(parent.UserSourceID, f.UserSourceID)
			parent.Follows = append(parent.Follows, *f)
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取工单回复失败: %w", err)
	}
	result := make([]*canonical.Ticket, 0, len(order))
	for _, id := range order {
		result = append(result, tickets[id])
	}
	return result, nil
}

func scanTicket(rows *sql.Rows) (*canonical.Ticket, error) {
	var (
		id       int64
		title    sql.NullString
		content  sql.NullString
		userID   sql.NullInt64
		datetime sql.NullInt64
		status   sql.NullInt64
	)
	if err := rows.Scan(&id, &title, &content, &userID, &datetime, &status); err != nil {
		return nil, err
	}
	createdAt := orEpoch(unixToTime(datetime.Int64))
	return &canonical.Ticket{
		SourceID:     id,
		Title:        firstNonEmpty(strings.TrimSpace(title.String), fmt.Sprintf("SSPanel 工单 #%d", id)),
		Description:  content.String,
		UserSourceID: userID.Int64,
		Status:       ticketStatus(status.Int64),
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	}, nil
}

func scanTicketFollow(rows *sql.Rows) (*canonical.TicketFollow, int64, error) {
	var (
		id       int64
		title    sql.NullString
		content  sql.NullString
		rootID   sql.NullInt64
		userID   sql.NullInt64
		datetime sql.NullInt64
		status   sql.NullInt64
	)
	if err := rows.Scan(&id, &title, &content, &rootID, &userID, &datetime, &status); err != nil {
		return nil, 0, err
	}
	createdAt := orEpoch(unixToTime(datetime.Int64))
	return &canonical.TicketFollow{
		SourceID:       id,
		TicketSourceID: rootID.Int64,
		UserSourceID:   userID.Int64,
		Content:        content.String,
		Type:           1,
		CreatedAt:      orEpoch(createdAt),
	}, rootID.Int64, nil
}

// ticketFollowFrom 回复人归属：与主单发起人相同 → user，否则视为管理员/客服。
func ticketFollowFrom(ownerID, followerID int64) string {
	if followerID == ownerID {
		return "user"
	}
	return "admin"
}

// ticketStatus 源状态 1=开启 → NPanel 1，其他（0/2…）→ 4 关闭。
func ticketStatus(status int64) int8 {
	if status == 1 {
		return 1
	}
	return 4
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
