package sspanelv3

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"

	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/db"
)

// ExtractSubscriptionsAtPoolUntil 从 user 当前状态 + 最近一次 bought 推导订阅。
//
// ss-panel v3 没有订阅表，可用性规则（源码 Mod_Mu/UserController + URL.php 核实）：
//   - enable=1 且 expire_in > now（账号有效）
//   - class_expire > now 且 class 匹配节点 node_class（套餐等级有效）
//
// 推导规则：
//   - 任一到期字段命中 1989 哨兵或已过 → NeedsTrial（与 v2board 已过期用户一致）。
//   - 套餐优先取最近一次 bought 的 shop，无 bought 按 shop.content.class == user.class
//     匹配最新商品；仍无 → NeedsTrial。
//   - 两个到期字段均为永久哨兵（2099/3001）→ 源端永久（ExpireTime=nil，writer 转+1月）。
func ExtractSubscriptionsAtPoolUntil(
	ctx context.Context,
	pool *sql.DB,
	evaluationTime time.Time,
	userHighWater, orderHighWater int64,
) ([]*canonical.UserSubscription, error) {
	// class → shop 的兜底映射（无 bought 但等级被手工设置的用户）。
	classShops, err := loadLatestShopPerClass(ctx, pool)
	if err != nil {
		return nil, err
	}

	var subs []*canonical.UserSubscription
	query := `SELECT u.id, u.class, u.class_expire, u.expire_in,
	                 u.transfer_enable, u.u, u.d, u.node_group, u.reg_date,
	                 b.id, b.shopid, b.datetime
	            FROM user u
	            LEFT JOIN bought b
	              ON b.id = (SELECT MAX(b2.id) FROM bought b2
	                          WHERE b2.userid = u.id AND b2.id <= ?)
	           WHERE u.enable = 1 AND u.id <= ?
	           ORDER BY u.id`
	err = db.QueryRowsDBWithTimeout(ctx, pool, 10*time.Minute, query,
		func(rows *sql.Rows) error {
			s, err := scanSubscriptionAt(rows, evaluationTime, classShops)
			if err != nil {
				return err
			}
			subs = append(subs, s)
			return nil
		},
		orderHighWater, userHighWater,
	)
	if err != nil {
		return nil, fmt.Errorf("读取订阅数据失败: %w", err)
	}
	return subs, nil
}

// loadLatestShopPerClass 读取每个 class 等级最新的在售商品 id（class 兜底用）。
func loadLatestShopPerClass(ctx context.Context, pool *sql.DB) (map[int64]int64, error) {
	result := make(map[int64]int64)
	err := db.QueryRowsDBWithTimeout(ctx, pool, time.Minute,
		`SELECT content, MAX(id) FROM shop WHERE status = 1 GROUP BY content`,
		func(rows *sql.Rows) error {
			var content sql.NullString
			var id sql.NullInt64
			if err := rows.Scan(&content, &id); err != nil {
				return err
			}
			c := parseShopContent(content.String)
			if c.Class > 0 && id.Int64 > 0 {
				// 同一 content 字符串分组后取最大 id；class 维度再取最新。
				if existing, ok := result[c.Class]; !ok || id.Int64 > existing {
					result[c.Class] = id.Int64
				}
			}
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取 class 商品映射失败: %w", err)
	}
	return result, nil
}

func scanSubscriptionAt(
	rows *sql.Rows,
	evaluationTime time.Time,
	classShops map[int64]int64,
) (*canonical.UserSubscription, error) {
	var (
		userID        int64
		class         sql.NullInt64
		classExpire   sql.NullString
		expireIn      sql.NullString
		transferLimit sql.NullInt64
		upload        sql.NullInt64
		download      sql.NullInt64
		nodeGroup     sql.NullInt64
		regDate       sql.NullString
		boughtID      sql.NullInt64
		shopID        sql.NullInt64
		boughtTime    sql.NullInt64
	)
	if err := rows.Scan(&userID, &class, &classExpire, &expireIn,
		&transferLimit, &upload, &download, &nodeGroup, &regDate,
		&boughtID, &shopID, &boughtTime); err != nil {
		return nil, err
	}

	expireTime, active := effectiveExpire(classExpire.String, expireIn.String, evaluationTime)
	planID, sourcePeriod, needsTrial := deriveSubscriptionPlan(
		active, class.Int64, shopID.Int64, classShops,
	)

	startTime := unixToTime(boughtTime.Int64)
	if startTime.IsZero() {
		startTime = parseDateTime(regDate.String)
	}
	if startTime.IsZero() {
		startTime = evaluationTime
	}

	return &canonical.UserSubscription{
		SourceID:      userID, // 源端用 user id 作为订阅标识（与 v2board 一致）
		UserSourceID:  userID,
		PlanSourceID:  planID,
		OrderSourceID: boughtID.Int64,
		GroupSourceID: subscriptionGroupID(nodeGroup.Int64),
		// 源面板无全局订阅 token（link 表是每节点分享链接），生成新 token/UUID。
		Token:         randomToken(),
		UUID:          uuid.NewString(),
		StartTime:     startTime,
		CreatedAt:     startTime,
		ExpireTime:    expireTime,
		TrafficBytes:  transferLimit.Int64,
		UploadBytes:   upload.Int64,
		DownloadBytes: download.Int64,
		Status:        1,
		NeedsTrial:    needsTrial,
		SourcePeriod:  conditionalPeriod(needsTrial, sourcePeriod),
	}, nil
}

// deriveSubscriptionPlan 订阅的套餐推导（纯函数，便于单测）：
//   - 已失效（哨兵/过期）→ NeedsTrial；
//   - 优先最近一次 bought 的商品（SourcePeriod=shop_price，要求价格档位映射）；
//   - 无购买记录按 class 匹配商品（无订单佐证，不强制价格档位映射）；
//   - 仍无 → NeedsTrial。
func deriveSubscriptionPlan(
	active bool,
	class, latestShopID int64,
	classShops map[int64]int64,
) (planID int64, sourcePeriod string, needsTrial bool) {
	if !active {
		return 0, "", true
	}
	if latestShopID > 0 {
		return latestShopID, shopPriceSourceKey, false
	}
	if id := classShops[class]; id > 0 {
		return id, "", false
	}
	return 0, "", true
}

// subscriptionGroupID user.node_group>0 → 合成分组 ID（1000+g），0 → 基础组。
func subscriptionGroupID(group int64) int64 {
	if group > 0 {
		return nodeGroupIDFromGroup(group)
	}
	return 0
}

func conditionalPeriod(needsTrial bool, period string) string {
	if needsTrial {
		return ""
	}
	return period
}

// randomToken 生成 32 位随机 hex 订阅 token。
func randomToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// 退化为时间熵（极小概率事件）。
		return hex.EncodeToString([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))[:32]
	}
	return hex.EncodeToString(buf)
}

// unixToTime Unix 秒 → time.Time（0 → 零值）。
func unixToTime(unix int64) time.Time {
	if unix <= 0 {
		return time.Time{}
	}
	return time.Unix(unix, 0).UTC()
}
