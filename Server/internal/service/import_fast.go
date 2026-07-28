package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/npanel-dev/NPanel-backend/ent"

	"npanel-migrator/internal/adapter/xiaov2board"
	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/checkpoint"
	"npanel-migrator/internal/data/db"
	"npanel-migrator/internal/data/detector"
	"npanel-migrator/internal/data/progress"
	"npanel-migrator/internal/data/writer"
)

var errMigrationCancelled = errors.New("migration cancelled")

type storedImportOptions struct {
	Modules         []string        `json:"modules"`
	PlanMappings    []PlanMapping   `json:"planMappings"`
	TrialAssignment TrialAssignment `json:"trialAssignment"`
}

func (s *MigrationService) runImportFast(
	req *ImportRequest,
	batchSize int,
	jobID, owner string,
) {
	defer clearActiveImport(jobID)

	ctx := context.Background()
	sourceCfg := db.Config{
		Host: req.SourceHost, Port: req.SourcePort, Database: req.SourceDatabase,
		Username: req.SourceUsername, Password: req.SourcePassword,
	}
	targetCfg := writer.NPanelConfig{
		Host: req.TargetHost, Port: req.TargetPort, Database: req.TargetDatabase,
		Username: req.TargetUsername, Password: req.TargetPassword,
	}

	result, err := detector.Detect(ctx, sourceCfg, req.SourcePanel)
	if err != nil {
		failFast(nil, jobID, "面板探测失败: "+err.Error())
		return
	}
	if !isV2boardFamily(result.Panel) {
		failFast(nil, jobID, fmt.Sprintf(
			"已识别为 %s 面板，但该 adapter 暂未实现（当前支持 xiaov2board/v2board）",
			result.Panel,
		))
		return
	}

	sourcePool, err := db.OpenPool(ctx, sourceCfg)
	if err != nil {
		failFast(nil, jobID, "连接源库失败: "+err.Error())
		return
	}
	defer sourcePool.Close()

	globalTracker.LogInfo("正在初始化目标库与迁移断点表...")
	globalTracker.Update(progress.PhaseInit, "初始化目标库", 0, 1, 0)
	if err := writer.EnsureSchema(ctx, targetCfg); err != nil {
		failFast(nil, jobID, "初始化目标库失败: "+err.Error())
		return
	}
	runtime, err := writer.OpenRuntime(ctx, targetCfg)
	if err != nil {
		failFast(nil, jobID, "连接目标库失败: "+err.Error())
		return
	}
	defer runtime.Close()

	store := checkpoint.New(runtime.DB)
	if err := store.EnsureSchema(ctx); err != nil {
		failFast(nil, jobID, err.Error())
		return
	}
	attachActiveStore(jobID, store)

	job, err := prepareMigrationJob(
		ctx, store, sourcePool, sourceCfg, targetCfg,
		string(result.Panel), req, jobID, owner,
	)
	if err != nil {
		failFast(store, jobID, "准备迁移任务失败: "+err.Error())
		return
	}
	store.BindOwner(owner)
	leaseCtx, stopLease := context.WithCancel(ctx)
	defer stopLease()
	go renewMigrationLease(leaseCtx, store, job.ID, owner)
	globalTracker.LogInfo(fmt.Sprintf("迁移任务 ID: %s", job.ID))
	if req.ResumeJobID != "" {
		globalTracker.LogInfo(fmt.Sprintf("从中断任务恢复，当前阶段：%s", job.Phase))
	}

	sourceMap := canonical.NewSourceMap()
	if err := configurePlanMappings(ctx, runtime.Client, req.PlanMappings, sourceMap); err != nil {
		failFast(store, jobID, "套餐映射校验失败: "+err.Error())
		return
	}

	userMappings, err := store.LoadMappings(ctx, jobID, "user")
	if err != nil {
		failFast(store, jobID, "加载用户映射失败: "+err.Error())
		return
	}
	for sourceID, targetID := range userMappings {
		sourceMap.UserIDs[sourceID] = targetID
	}
	orderMappings, err := store.LoadMappings(ctx, jobID, "order")
	if err != nil {
		failFast(store, jobID, "加载订单映射失败: "+err.Error())
		return
	}
	for sourceID, targetID := range orderMappings {
		sourceMap.OrderIDs[sourceID] = targetID
	}

	totalErrors := int(job.Errors)
	modules := req.Modules

	if hasModule(modules, ModuleNodes) {
		groups, err := xiaov2board.ExtractNodeGroups(ctx, sourceCfg)
		if err != nil {
			failFast(store, jobID, "读取节点分组失败: "+err.Error())
			return
		}
		for _, group := range groups {
			sourceMap.NodeGroupIDs[group.SourceID] = group.SourceID
		}
		totalErrors, err = runAtomicPhase(
			ctx, runtime, store, jobID, owner,
			"nodeGroups", progress.PhaseNodes, "迁移节点分组",
			len(groups), totalErrors,
			func(client *ent.Client) (int, int, error) {
				_, written, err := writer.WriteNodeGroups(ctx, client, groups)
				return written, len(groups) - written, err
			},
		)
		if err != nil {
			handleFastError(store, jobID, err, "迁移节点分组失败")
			return
		}
	}

	var preparedSubscriptions []*canonical.UserSubscription
	if hasModule(modules, ModuleSubscriptions) {
		preparedSubscriptions, err = xiaov2board.ExtractSubscriptionsAtPoolUntil(
			ctx, sourcePool, sourceCfg.Database, job.TrialAnchorTime,
			job.UserHighWater, job.OrderHighWater,
		)
		if err != nil {
			failFast(store, jobID, "读取订阅分配数据失败: "+err.Error())
			return
		}
		if err := validateSubscriptionAssignments(
			ctx, runtime.Client, preparedSubscriptions, sourceMap, req.TrialAssignment,
		); err != nil {
			failFast(store, jobID, "订阅分配预检失败: "+err.Error())
			return
		}
	}

	if hasModule(modules, ModuleUsers) {
		totalUsers, err := db.QueryScalarDBWithTimeout(
			ctx, sourcePool, 30*time.Second,
			"SELECT COUNT(*) FROM v2_user WHERE id <= ?", job.UserHighWater,
		)
		if err != nil {
			failFast(store, jobID, "统计用户失败: "+err.Error())
			return
		}
		userCP, err := store.LoadCheckpoint(ctx, jobID, "users")
		if err != nil {
			failFast(store, jobID, "读取用户断点失败: "+err.Error())
			return
		}
		userCP.Total = totalUsers
		if userCP.Errors < int64(totalErrors) {
			userCP.Errors = int64(totalErrors)
		}
		globalTracker.Update(
			progress.PhaseUsers, "迁移用户",
			int(userCP.Done), int(userCP.Total), int(userCP.Errors),
		)
		err = xiaov2board.ExtractUsersKeyset(
			ctx, sourcePool, batchSize,
			userCP.LastSourceID, job.UserHighWater,
			func(batch []*canonical.User) error {
				if cancellationRequested(ctx, store, jobID) {
					return errMigrationCancelled
				}
				setUserSourcePanel(batch, string(result.Panel))
				idMap, _, err := writer.WriteUsersBulk(
					ctx, runtime, store, jobID, owner, batch, &userCP,
				)
				if err != nil {
					return err
				}
				for sourceID, targetID := range idMap {
					sourceMap.UserIDs[sourceID] = targetID
				}
				globalTracker.Update(
					progress.PhaseUsers, "迁移用户",
					int(userCP.Done), int(userCP.Total), int(userCP.Errors),
				)
				return nil
			},
		)
		if err != nil {
			handleFastError(store, jobID, err, "迁移用户失败")
			return
		}
		totalErrors = int(userCP.Errors)

		referCP, err := store.LoadCheckpoint(ctx, jobID, "refererBackfill")
		if err != nil {
			failFast(store, jobID, "读取邀请关系断点失败: "+err.Error())
			return
		}
		referCP.Total = totalUsers
		if referCP.Errors < int64(totalErrors) {
			referCP.Errors = int64(totalErrors)
		}
		globalTracker.Update(
			progress.PhaseReferBackfill, "回填邀请关系",
			int(referCP.Done), int(referCP.Total), int(referCP.Errors),
		)
		err = xiaov2board.ExtractUsersKeyset(
			ctx, sourcePool, batchSize,
			referCP.LastSourceID, job.UserHighWater,
			func(batch []*canonical.User) error {
				if cancellationRequested(ctx, store, jobID) {
					return errMigrationCancelled
				}
				_, err := writer.BackfillReferersBulk(
					ctx, runtime, store, jobID, owner,
					batch, sourceMap.UserIDs, &referCP,
				)
				if err == nil {
					globalTracker.Update(
						progress.PhaseReferBackfill, "回填邀请关系",
						int(referCP.Done), int(referCP.Total), int(referCP.Errors),
					)
				}
				return err
			},
		)
		if err != nil {
			handleFastError(store, jobID, err, "回填邀请关系失败")
			return
		}
		totalErrors = int(referCP.Errors)
	}

	if hasModule(modules, ModuleOrders) {
		totalOrders, err := db.QueryScalarDBWithTimeout(
			ctx, sourcePool, 30*time.Second,
			"SELECT COUNT(*) FROM v2_order WHERE id <= ?", job.OrderHighWater,
		)
		if err != nil {
			failFast(store, jobID, "统计订单失败: "+err.Error())
			return
		}
		orderCP, err := store.LoadCheckpoint(ctx, jobID, "orders")
		if err != nil {
			failFast(store, jobID, "读取订单断点失败: "+err.Error())
			return
		}
		orderCP.Total = totalOrders
		if orderCP.Errors < int64(totalErrors) {
			orderCP.Errors = int64(totalErrors)
		}
		globalTracker.Update(
			progress.PhaseOrders, "迁移订单",
			int(orderCP.Done), int(orderCP.Total), int(orderCP.Errors),
		)
		err = xiaov2board.ExtractOrdersKeyset(
			ctx, sourcePool, batchSize,
			orderCP.LastSourceID, job.OrderHighWater,
			func(batch []*canonical.Order) error {
				if cancellationRequested(ctx, store, jobID) {
					return errMigrationCancelled
				}
				idMap, _, err := writer.WriteOrdersBulk(
					ctx, runtime, store, jobID, owner,
					batch, sourceMap, &orderCP,
				)
				if err != nil {
					return err
				}
				for sourceID, targetID := range idMap {
					sourceMap.OrderIDs[sourceID] = targetID
				}
				globalTracker.Update(
					progress.PhaseOrders, "迁移订单",
					int(orderCP.Done), int(orderCP.Total), int(orderCP.Errors),
				)
				return nil
			},
		)
		if err != nil {
			handleFastError(store, jobID, err, "迁移订单失败")
			return
		}
		totalErrors = int(orderCP.Errors)
	}

	if hasModule(modules, ModuleSubscriptions) {
		subCP, err := store.LoadCheckpoint(ctx, jobID, "subscriptions")
		if err != nil {
			failFast(store, jobID, "读取订阅断点失败: "+err.Error())
			return
		}
		subCP.Total = int64(len(preparedSubscriptions))
		if subCP.Errors < int64(totalErrors) {
			subCP.Errors = int64(totalErrors)
		}
		globalTracker.Update(
			progress.PhaseSubscriptions, "迁移订阅",
			int(subCP.Done), int(subCP.Total), int(subCP.Errors),
		)
		planCache := make(map[int64]*ent.ProxySubscribe)
		for start := 0; start < len(preparedSubscriptions); {
			for start < len(preparedSubscriptions) &&
				preparedSubscriptions[start].SourceID <= subCP.LastSourceID {
				start++
			}
			if start >= len(preparedSubscriptions) {
				break
			}
			if cancellationRequested(ctx, store, jobID) {
				handleFastError(store, jobID, errMigrationCancelled, "迁移订阅已取消")
				return
			}
			end := start + batchSize
			if end > len(preparedSubscriptions) {
				end = len(preparedSubscriptions)
			}
			_, _, err := writer.WriteSubscriptionsBulk(
				ctx, runtime, store, jobID, owner,
				preparedSubscriptions[start:end], sourceMap,
				writer.TrialAssignment{
					SubscribeID:   req.TrialAssignment.TargetSubscribeID,
					DurationUnit:  req.TrialAssignment.DurationUnit,
					DurationValue: req.TrialAssignment.DurationValue,
				},
				job.TrialAnchorTime, planCache, &subCP,
			)
			if err != nil {
				handleFastError(store, jobID, err, "迁移订阅失败")
				return
			}
			globalTracker.Update(
				progress.PhaseSubscriptions, "迁移订阅",
				int(subCP.Done), int(subCP.Total), int(subCP.Errors),
			)
			start = end
		}
		totalErrors = int(subCP.Errors)
	}

	if hasModule(modules, ModuleNodes) {
		nodes, err := xiaov2board.ExtractNodes(ctx, sourceCfg)
		if err != nil {
			failFast(store, jobID, "读取节点失败: "+err.Error())
			return
		}
		totalErrors, err = runAtomicPhase(
			ctx, runtime, store, jobID, owner,
			"nodes", progress.PhaseNodes, "迁移节点",
			len(nodes), totalErrors,
			func(client *ent.Client) (int, int, error) {
				written, err := writer.WriteNodes(ctx, client, nodes, sourceMap)
				return written, len(nodes) - written, err
			},
		)
		if err != nil {
			handleFastError(store, jobID, err, "迁移节点失败")
			return
		}
	}

	if hasModule(modules, ModuleCoupons) {
		coupons, err := xiaov2board.ExtractCoupons(ctx, sourceCfg)
		if err != nil {
			failFast(store, jobID, "读取优惠券失败: "+err.Error())
			return
		}
		totalErrors, err = runAtomicPhase(
			ctx, runtime, store, jobID, owner,
			"coupons", progress.PhaseCoupons, "迁移优惠券",
			len(coupons), totalErrors,
			func(client *ent.Client) (int, int, error) {
				written, err := writer.WriteCoupons(ctx, client, coupons)
				return written, len(coupons) - written, err
			},
		)
		if err != nil {
			handleFastError(store, jobID, err, "迁移优惠券失败")
			return
		}
	}

	if hasModule(modules, ModuleNotices) {
		notices, err := xiaov2board.ExtractNotices(ctx, sourceCfg)
		if err != nil {
			failFast(store, jobID, "读取公告失败: "+err.Error())
			return
		}
		totalErrors, err = runAtomicPhase(
			ctx, runtime, store, jobID, owner,
			"notices", progress.PhaseNotices, "迁移公告",
			len(notices), totalErrors,
			func(client *ent.Client) (int, int, error) {
				written, err := writer.WriteNotices(ctx, client, notices)
				return written, len(notices) - written, err
			},
		)
		if err != nil {
			handleFastError(store, jobID, err, "迁移公告失败")
			return
		}
	}

	if hasModule(modules, ModuleTickets) {
		tickets, err := xiaov2board.ExtractTickets(ctx, sourceCfg)
		if err != nil {
			failFast(store, jobID, "读取工单失败: "+err.Error())
			return
		}
		totalErrors, err = runAtomicPhase(
			ctx, runtime, store, jobID, owner,
			"tickets", progress.PhaseTickets, "迁移工单",
			len(tickets), totalErrors,
			func(client *ent.Client) (int, int, error) {
				written, err := writer.WriteTickets(ctx, client, tickets, sourceMap)
				return written, len(tickets) - written, err
			},
		)
		if err != nil {
			handleFastError(store, jobID, err, "迁移工单失败")
			return
		}
	}

	if cancellationRequested(ctx, store, jobID) {
		handleFastError(store, jobID, errMigrationCancelled, "迁移已取消")
		return
	}
	message := fmt.Sprintf("迁移完成（累计错误 %d，详见迁移错误账本）", totalErrors)
	if err := store.MarkFinished(ctx, jobID, checkpoint.StatusCompleted, ""); err != nil {
		failFast(store, jobID, "标记迁移完成失败: "+err.Error())
		return
	}
	globalTracker.Complete(message)
}

func prepareMigrationJob(
	ctx context.Context,
	store *checkpoint.Store,
	sourcePool *sql.DB,
	sourceCfg db.Config,
	targetCfg writer.NPanelConfig,
	panel string,
	req *ImportRequest,
	jobID, owner string,
) (*checkpoint.Job, error) {
	optionsJSON, optionsHash, err := stableOptions(req)
	if err != nil {
		return nil, err
	}
	sourceKey := configFingerprint(panel, sourceCfg.Host, sourceCfg.Port, sourceCfg.Database)
	targetKey := configFingerprint("npanel", targetCfg.Host, targetCfg.Port, targetCfg.Database)

	if req.ResumeJobID != "" {
		job, err := store.GetJob(ctx, req.ResumeJobID)
		if err != nil {
			return nil, err
		}
		if job.SourceKey != sourceKey || job.TargetKey != targetKey || job.OptionsHash != optionsHash {
			return nil, errors.New("恢复任务的源库、目标库或迁移选项与原任务不一致")
		}
		if err := store.AcquireJob(ctx, job.ID, owner); err != nil {
			return nil, err
		}
		job.Status = checkpoint.StatusRunning
		job.LeaseOwner = owner
		job.OptionsJSON = optionsJSON
		return job, nil
	}

	userHighWater, err := db.QueryScalarDBWithTimeout(
		ctx, sourcePool, 30*time.Second,
		"SELECT COALESCE(MAX(id), 0) FROM v2_user",
	)
	if err != nil {
		return nil, err
	}
	orderHighWater, err := db.QueryScalarDBWithTimeout(
		ctx, sourcePool, 30*time.Second,
		"SELECT COALESCE(MAX(id), 0) FROM v2_order",
	)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	job := &checkpoint.Job{
		ID: jobID, SourceKey: sourceKey, TargetKey: targetKey,
		OptionsHash: optionsHash, OptionsJSON: optionsJSON,
		Status: checkpoint.StatusRunning, Phase: "init",
		UserHighWater: userHighWater, OrderHighWater: orderHighWater,
		TrialAnchorTime: now, LeaseOwner: owner,
		StartedAt: now, UpdatedAt: now,
	}
	if err := store.CreateJob(ctx, *job); err != nil {
		return nil, err
	}
	return job, nil
}

func runAtomicPhase(
	ctx context.Context,
	runtime *writer.Runtime,
	store *checkpoint.Store,
	jobID, owner, phase string,
	progressPhase progress.Phase,
	label string,
	total, priorErrors int,
	write func(*ent.Client) (written, failed int, err error),
) (int, error) {
	cp, err := store.LoadCheckpoint(ctx, jobID, phase)
	if err != nil {
		return priorErrors, err
	}
	cp.Total = int64(total)
	if cp.Errors < int64(priorErrors) {
		cp.Errors = int64(priorErrors)
	}
	globalTracker.Update(progressPhase, label, int(cp.Done), total, int(cp.Errors))
	if cp.Done >= int64(total) {
		return int(cp.Errors), nil
	}
	if cancellationRequested(ctx, store, jobID) {
		return int(cp.Errors), errMigrationCancelled
	}
	tx, err := runtime.BeginTx(ctx)
	if err != nil {
		return int(cp.Errors), err
	}
	defer tx.Rollback()
	written, failed, err := write(tx.Client)
	if err != nil {
		return int(cp.Errors), err
	}
	next := cp
	next.Done = int64(total)
	next.LastSourceID = int64(total)
	next.Errors += int64(failed)
	if err := store.RecordBatchTx(ctx, tx.SQL, checkpoint.BatchRecord{
		JobID: jobID, Phase: phase,
		CursorFrom: 0, CursorTo: int64(total),
		Attempted: total, Succeeded: written, Failed: failed, Status: "committed",
	}); err != nil {
		return int(cp.Errors), err
	}
	if err := store.SaveCheckpointTx(ctx, tx.SQL, next, owner); err != nil {
		return int(cp.Errors), err
	}
	if err := tx.Commit(); err != nil {
		return int(cp.Errors), err
	}
	globalTracker.Update(progressPhase, label, total, total, int(next.Errors))
	return int(next.Errors), nil
}

func stableOptions(req *ImportRequest) (string, string, error) {
	modules := append([]string(nil), req.Modules...)
	if len(modules) == 0 {
		modules = append([]string(nil), AllModules...)
	}
	sort.Strings(modules)
	mappings := append([]PlanMapping(nil), req.PlanMappings...)
	for index := range mappings {
		mappings[index].PeriodMappings = append(
			[]PeriodMapping(nil), mappings[index].PeriodMappings...,
		)
		sort.Slice(mappings[index].PeriodMappings, func(i, j int) bool {
			return mappings[index].PeriodMappings[i].SourcePeriod <
				mappings[index].PeriodMappings[j].SourcePeriod
		})
	}
	sort.Slice(mappings, func(i, j int) bool {
		return mappings[i].SourcePlanID < mappings[j].SourcePlanID
	})
	payload, err := json.Marshal(storedImportOptions{
		Modules: modules, PlanMappings: mappings, TrialAssignment: req.TrialAssignment,
	})
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(payload)
	return string(payload), hex.EncodeToString(sum[:]), nil
}

func configFingerprint(kind, host string, port int, database string) string {
	normalized := fmt.Sprintf(
		"%s|%s|%d|%s",
		strings.ToLower(strings.TrimSpace(kind)),
		strings.ToLower(strings.TrimSpace(host)),
		port,
		strings.ToLower(strings.TrimSpace(database)),
	)
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func hydrateResumeOptions(ctx context.Context, req *ImportRequest) error {
	targetCfg := writer.NPanelConfig{
		Host: req.TargetHost, Port: req.TargetPort, Database: req.TargetDatabase,
		Username: req.TargetUsername, Password: req.TargetPassword,
	}
	runtime, err := writer.OpenRuntime(ctx, targetCfg)
	if err != nil {
		return err
	}
	defer runtime.Close()
	store := checkpoint.New(runtime.DB)
	if err := store.EnsureSchema(ctx); err != nil {
		return err
	}
	job, err := store.GetJob(ctx, strings.TrimSpace(req.ResumeJobID))
	if err != nil {
		return err
	}
	if !job.Resumable {
		return errors.New("该任务不可恢复，可能已完成或仍由其他迁移器运行")
	}
	var options storedImportOptions
	if err := json.Unmarshal([]byte(job.OptionsJSON), &options); err != nil {
		return fmt.Errorf("解析原迁移选项失败: %w", err)
	}
	req.Modules = options.Modules
	req.PlanMappings = options.PlanMappings
	req.TrialAssignment = options.TrialAssignment
	return nil
}

func renewMigrationLease(ctx context.Context, store *checkpoint.Store, jobID, owner string) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := store.RenewLease(renewCtx, jobID, owner)
			cancel()
			if err != nil && ctx.Err() == nil {
				globalTracker.LogWarn("刷新迁移任务租约失败: " + err.Error())
			}
		}
	}
}

func attachActiveStore(jobID string, store *checkpoint.Store) {
	activeImport.Lock()
	defer activeImport.Unlock()
	if activeImport.jobID == jobID {
		activeImport.store = store
	}
}

func clearActiveImport(jobID string) {
	activeImport.Lock()
	defer activeImport.Unlock()
	if activeImport.jobID == jobID {
		activeImport.jobID = ""
		activeImport.store = nil
		activeImport.cancelRequested = false
	}
}

func cancellationRequested(ctx context.Context, store *checkpoint.Store, jobID string) bool {
	activeImport.Lock()
	local := activeImport.jobID == jobID && activeImport.cancelRequested
	activeImport.Unlock()
	if local {
		return true
	}
	requested, err := store.IsCancelRequested(ctx, jobID)
	return err == nil && requested
}

func handleFastError(store *checkpoint.Store, jobID string, err error, prefix string) {
	if errors.Is(err, errMigrationCancelled) {
		_ = store.MarkFinished(context.Background(), jobID, checkpoint.StatusCancelled, "用户安全取消")
		globalTracker.Cancel("迁移已安全取消，可使用同一任务 ID 恢复")
		return
	}
	failFast(store, jobID, prefix+": "+err.Error())
}

func failFast(store *checkpoint.Store, jobID, message string) {
	if store != nil {
		_ = store.MarkFinished(context.Background(), jobID, checkpoint.StatusFailed, message)
	}
	globalTracker.SetResumable(store != nil && store.Owned())
	globalTracker.Fail(message)
	globalTracker.LogError(message)
}
