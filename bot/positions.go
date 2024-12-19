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
	q querier
	e *eventer

	plans map[uint64]types.Plan
	plmu  sync.Mutex

	logger *zap.Logger
}

func newPositionManager(q querier, e *eventer, logger *zap.Logger) *positionManager {
	return &positionManager{
		q:      q,
		e:      e,
		plans:  make(map[uint64]types.Plan),
		logger: logger,
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

	return nil
}

func (pm *positionManager) iteratePlans(f func(types.Plan) error) error {
	pm.plmu.Lock()
	defer pm.plmu.Unlock()

	for _, p := range pm.plans {
		if err := f(p); err != nil {
			return err
		}
	}

	return nil
}

func (pm *positionManager) pollPlans(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()

	for {
		select {
		case <-t.C:
			plans, err := pm.q.queryIROPlans(ctx)
			if err != nil {
				panic(err)
			}

			pm.plmu.Lock()
			for _, p := range plans {
				pm.plans[p.Id] = p
			}
			pm.plmu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}
