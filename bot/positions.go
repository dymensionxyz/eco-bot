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

	plans []types.Plan
	plmu  sync.RWMutex

	logger *zap.Logger
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
	if err := pm.e.start(ctx); err != nil {
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

			pm.plans[planId] = *plan
		}
		return nil
	}

	if err := pm.e.subscribeToCreatedIROs(ctx, callback); err != nil {
		return fmt.Errorf("subscribe to created IROs: %w", err)
	}

	go pm.pollPlans(ctx)

	return nil
}

func (pm *positionManager) iteratePlans(f func(types.Plan) error) error {
	pm.plmu.RLock()
	defer pm.plmu.RUnlock()

	for _, p := range pm.plans {
		if err := f(p); err != nil {
			return err
		}
	}

	return nil
}

func (pm *positionManager) getIROPlans(ctx context.Context) {
	plans, err := pm.q.queryIROPlans(ctx)
	if err != nil {
		pm.logger.Error("query IRO plans", zap.Error(err))
		return
	}

	pm.plmu.Lock()
	pm.plans = plans
	pm.plmu.Unlock()
}

func (pm *positionManager) pollPlans(ctx context.Context) {
	pm.getIROPlans(ctx)

	t := time.NewTicker(pm.planPollingInterval)
	defer t.Stop()

	for {
		select {
		case <-t.C:
			pm.getIROPlans(ctx)
			pm.logger.Info("got IRO plans", zap.Int("count", len(pm.plans)))
		case <-ctx.Done():
			return
		}
	}
}
