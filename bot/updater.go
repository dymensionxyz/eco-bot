package bot

import (
	"context"
	"fmt"
	"sync"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"go.uber.org/zap"
)

type updater struct {
	prices  map[uint64]sdk.Dec
	volumes map[uint64]sdk.Dec
	sync.Mutex
	interval time.Duration
	q        querier
	// logger   *zap.Logger
}

func newUpdater(q querier, interval time.Duration, logger *zap.Logger) *updater {
	return &updater{
		prices:   make(map[uint64]sdk.Dec),
		volumes:  make(map[uint64]sdk.Dec),
		q:        q,
		interval: interval,
		// logger:   logger,
	}
}

func (u *updater) start(ctx context.Context) {
	plans, err := u.q.queryIROPlans(ctx)
	if err != nil {
		panic(err)
	}

	u.Lock()
	for _, p := range plans {
		price, err := u.q.querySpotPrice(ctx, fmt.Sprint(p.Id))
		if err != nil {
			panic(err)
		}
		u.prices[p.Id] = price
		// u.volumes[p.ID] = sdk.ZeroDec()
	}
	u.Unlock()

	go u.updatePrices(ctx)
	// go u.updateVolumes() TODO
}

func (u *updater) updatePrices(ctx context.Context) {
	t := time.NewTicker(u.interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			u.Lock()
			for planID := range u.prices {
				price, err := u.q.querySpotPrice(ctx, fmt.Sprint(planID))
				if err != nil {
					panic(err)
				}
				u.prices[planID] = price
			}
			u.Unlock()
		}
	}
}

func (u *updater) updateVolumes() {
	for {
		u.Lock()
		for planID := range u.volumes {
			/*volume, err := u.q.queryTokensForDYM(planID)
			if err != nil {
				panic(err)
			}*/
			u.volumes[planID] = sdk.Dec{}
		}
		u.Unlock()
	}
}
