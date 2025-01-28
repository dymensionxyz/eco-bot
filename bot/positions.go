package bot

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	tmtypes "github.com/tendermint/tendermint/rpc/core/types"
	"go.uber.org/zap"

	"github.com/dymensionxyz/eco-bot/types"
)

type positionManager struct {
	q                   querier
	e                   *eventer
	planPollingInterval time.Duration

	plans []iroPlan
	plmu  sync.RWMutex

	logger *zap.Logger
}

type iroPlan struct {
	types.Plan
	analyticsResp
}

func newPositionManager(q querier, e *eventer, planPollingInterval time.Duration, logger *zap.Logger) *positionManager {
	return &positionManager{
		q:                   q,
		e:                   e,
		planPollingInterval: planPollingInterval,
		logger:              logger,
	}
}

func (pm *positionManager) start(ctx context.Context) error {
	if err := pm.e.start(); err != nil {
		return fmt.Errorf("start eventer: %w", err)
	}

	callback := func(ctx context.Context, name string, event tmtypes.ResultEvent) error {
		pm.logger.Info("new IRO plan", zap.Any("event", event))

		pm.plmu.Lock()
		defer pm.plmu.Unlock()

		ids := event.Events[name+".plan_id"]

		for _, id := range ids {
			planId, err := strconv.ParseUint(id, 10, 64)
			if err != nil {
				return fmt.Errorf("parse plan id: %w", err)
			}

			// TODO: get from event instead of querying
			plan, err := pm.q.queryIROPlan(ctx, id)
			if err != nil {
				return fmt.Errorf("query IRO plan: %w", err)
			}

			analytics, err := pm.q.queryAnalytics(plan.RollappId)
			if err != nil {
				return fmt.Errorf("query analytics: %w", err)
			}

			p := iroPlan{
				Plan:          *plan,
				analyticsResp: *analytics,
			}

			pm.plans[planId] = p
		}
		return nil
	}

	if err := pm.e.subscribeToCreatedIROs(ctx, callback); err != nil {
		return fmt.Errorf("subscribe to created IROs: %w", err)
	}

	pm.getIROPlans(ctx)

	go pm.pollPlans(ctx)

	return nil
}

func (pm *positionManager) iteratePlans(f iteratePlanCallback) error {
	plansCopy := make([]iroPlan, len(pm.plans))
	pm.plmu.RLock()
	copy(plansCopy, pm.plans)
	pm.plmu.RUnlock()

	for _, p := range plansCopy {
		if err := f(p); err != nil {
			return err
		}
	}

	return nil
}

func (pm *positionManager) getIROPlans(ctx context.Context) {
	pm.plmu.Lock()
	defer pm.plmu.Unlock()
	var err error
	pm.plans, err = pm.q.queryIROPlans(ctx)
	if err != nil {
		pm.logger.Error("query IRO plans", zap.Error(err))
		return
	}
}

func (pm *positionManager) pollPlans(ctx context.Context) {
	t := time.NewTicker(pm.planPollingInterval)
	defer t.Stop()

	for {
		select {
		case <-t.C:
			pm.getIROPlans(ctx)
			pm.logger.Debug("got IRO plans", zap.Int("count", len(pm.plans)))
		case <-ctx.Done():
			return
		}
	}
}
