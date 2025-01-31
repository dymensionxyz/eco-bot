package bot

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	tmtypes "github.com/tendermint/tendermint/rpc/core/types"
	"go.uber.org/zap"

	"github.com/dymensionxyz/eco-bot/types"
)

type positionManager struct {
	q               querier
	e               *eventer
	pollingInterval time.Duration

	gammPools        map[string]gammPool // denom -> pool
	plans            []iroPlan
	rollapps         []rollapp
	plmu, ramu, gpmu sync.RWMutex

	logger *zap.Logger
}

type iroPlan struct {
	types.Plan
	analyticsResp
}

func newPositionManager(q querier, e *eventer, pollingInterval time.Duration, logger *zap.Logger) *positionManager {
	return &positionManager{
		q:               q,
		e:               e,
		pollingInterval: pollingInterval,
		gammPools:       make(map[string]gammPool),
		logger:          logger,
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

	pm.loadGammPools()
	pm.getRollapps()
	pm.getIROPlans(ctx)

	go pm.poll(ctx)

	return nil
}

func (pm *positionManager) iteratePlans(f iteratePlanCallback) error {
	plansCopy := make([]iroPlan, len(pm.plans))
	pm.plmu.RLock()
	copy(plansCopy, pm.plans)
	pm.plmu.RUnlock()

	for _, p := range plansCopy {
		stop, err := f(p)
		if err != nil {
			return err
		}
		if stop {
			break
		}
	}

	return nil
}

func (pm *positionManager) iterateRollapps(f iterateRollappCallback) error {
	pm.ramu.RLock()
	rollappsCopy := make([]rollapp, len(pm.rollapps))
	copy(rollappsCopy, pm.rollapps)
	pm.ramu.RUnlock()

	for _, r := range rollappsCopy {
		stop, err := f(r)
		if err != nil {
			return err
		}
		if stop {
			break
		}
	}

	return nil
}

func (pm *positionManager) getRandomTopXPlan(x int) *iroPlan {
	topPlans := make(map[string]iroPlan, x)
	_ = pm.iteratePlans(func(p iroPlan) (bool, error) {
		topPlans[p.RollappId] = p
		if len(topPlans) >= x {
			return true, nil
		}
		return false, nil
	})

	for _, v := range topPlans {
		return &v
	}

	return nil
}

func (pm *positionManager) getRandomTopXRollapp(x int) *rollapp {
	topRollapps := make(map[string]rollapp, x)
	_ = pm.iterateRollapps(func(r rollapp) (bool, error) {
		topRollapps[r.ChainID] = r
		if len(topRollapps) >= x {
			return true, nil
		}
		return false, nil
	})

	for _, v := range topRollapps {
		return &v
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

func (pm *positionManager) getRollapps() {
	pm.ramu.Lock()
	defer pm.ramu.Unlock()
	rollapps, err := pm.q.queryRollapps()
	if err != nil {
		pm.logger.Error("query rollapps", zap.Error(err))
		return
	}

	pm.rollapps = make([]rollapp, 0, len(rollapps))

	for _, r := range rollapps {
		pm.gpmu.RLock()
		gp, ok := pm.gammPools[r.IBCDenom]
		pm.gpmu.RUnlock()
		if !ok {
			continue
		}
		var okInt bool
		r.Liquidity, okInt = sdk.NewIntFromString(gp.PoolAssets[0].Token.Amount)
		if !okInt {
			pm.logger.Error("parse liquidity", zap.String("amount", gp.PoolAssets[0].Token.Amount))
			continue
		}
		r.PoolID, err = strconv.ParseUint(gp.Id, 10, 64)
		if err != nil {
			pm.logger.Error("parse pool ID", zap.Error(err))
			continue
		}

		if r.PoolID > 0 {
			pm.rollapps = append(pm.rollapps, r)
		}
	}

	slices.SortFunc(pm.rollapps, func(r1, r2 rollapp) int {
		if r1.Liquidity.GT(r2.Liquidity) {
			return -1
		}
		if r1.Liquidity.LT(r2.Liquidity) {
			return 1
		}
		return 0
	})
}

func (pm *positionManager) loadGammPools() {
	resp, err := pm.q.queryGammPools()
	if err != nil {
		pm.logger.Error("query gamm pools", zap.Error(err))
	}

	pm.gpmu.Lock()
	for _, p := range resp.Pools {
		if len(p.PoolAssets) == 2 {
			pm.gammPools[p.PoolAssets[1].Token.Denom] = p
		}
	}
	pm.gpmu.Unlock()
}

func (pm *positionManager) removeGammPool(denom string) {
	pm.gpmu.Lock()
	delete(pm.gammPools, denom)
	pm.gpmu.Unlock()
}

func (pm *positionManager) poll(ctx context.Context) {
	t := time.NewTicker(pm.pollingInterval)
	defer t.Stop()

	for {
		select {
		case <-t.C:
			pm.getIROPlans(ctx)
			pm.logger.Debug("got IRO plans", zap.Int("count", len(pm.plans)))

			pm.getRollapps()
			pm.logger.Debug("got rollapps", zap.Int("count", len(pm.rollapps)))
		case <-ctx.Done():
			return
		}
	}
}
