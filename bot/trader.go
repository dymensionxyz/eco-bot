package bot

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"math/rand"
	"slices"
	"strings"
	"time"

	"github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/dymensionxyz/cosmosclient/cosmosclient"
	"go.uber.org/zap"

	"github.com/dymensionxyz/eco-bot/types"
)

type trader struct {
	accountSvc             *accountService
	client                 cosmosClient
	NAV                    sdk.Int // net asset value of all positions in DYM
	q                      querier
	iteratePlans           func(iteratePlanCallback) error
	iterateRollapps        func(callback iterateRollappCallback) error
	getRandomPlan          func(topX int) *iroPlan
	getRandomRollapp       func(topX int) *rollapp
	removeGammPool         func(string)
	positions              map[string]position
	getState               func() (*state, error)
	setState               func(*state) error
	buy                    func(context.Context, sdk.Int, sdk.Int, string) error
	sell                   func(context.Context, sdk.Int, sdk.Int, string) error
	getBalances            func(context.Context) (sdk.Coins, error)
	balanceDYM             func() sdk.Int
	positionManageInterval time.Duration
	bedtimeStartHour       int
	cooldownRangeMinutes   []int
	maxPositions           int
	logger                 *zap.Logger
}

type cosmosClient interface {
	BroadcastTx(accountName string, msgs ...sdk.Msg) (cosmosclient.Response, error)
	Context() client.Context
}

type plan interface {
	GetId() uint64
	GetRollappId() string
	TargetRaise() sdk.Dec
	TotalSoldInDYM() sdk.Int
	SpotPrice() sdk.Dec
	MinIncome(sdk.Int) sdk.Int
	MinAmount(sdk.Int) (sdk.Int, error)
}

var (
	DYM                         = sdk.NewIntFromBigInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	standardTargetRaise         = sdk.NewDec(10000)
	defaultBedtimeDurationHours = 8
)

const (
	maxDYMForTrade = 2 // TODO change
	minDYMForTrade = 1 // TODO change
)

func newTrader(
	accountSvc *accountService,
	q querier,
	iteratePlans func(iteratePlanCallback) error,
	iterateRollapps func(callback iterateRollappCallback) error,
	getRandomPlan func(int) *iroPlan,
	getRandomRollapp func(int) *rollapp,
	removeGammPool func(string),
	cClient cosmosClient,
	getState func() (*state, error),
	setState func(*state) error,
	positionManageInterval time.Duration,
	bedtimeStartHour int,
	cooldownRangeMinutes []int,
	maxPositions int,
	logger *zap.Logger,
) *trader {
	t := &trader{
		accountSvc:             accountSvc,
		client:                 cClient,
		q:                      q,
		iteratePlans:           iteratePlans,
		iterateRollapps:        iterateRollapps,
		getRandomPlan:          getRandomPlan,
		getRandomRollapp:       getRandomRollapp,
		removeGammPool:         removeGammPool,
		positions:              make(map[string]position),
		getState:               getState,
		setState:               setState,
		positionManageInterval: positionManageInterval,
		bedtimeStartHour:       bedtimeStartHour,
		cooldownRangeMinutes:   cooldownRangeMinutes,
		logger: logger.With(
			zap.String("trader", accountSvc.accountName),
			zap.String("address", accountSvc.address())),
		NAV:          sdk.NewInt(0),
		maxPositions: maxPositions,
	}
	t.buy = t.buyAmount
	t.sell = t.sellAmount
	t.getBalances = t.accountSvc.getAccountBalances
	t.balanceDYM = t.balanceOfDYM
	return t
}

// managePositions periodically traverses all the positions and applies logic to manage them by trading.
// logic is applied to decide what to do with current positions, and if any new positions should be opened.
func (t *trader) managePositions() {
	s, err := t.getState()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s = &state{
				// Positions: make(map[string]positionState), TODO: enable
			}
			if err := t.setState(s); err != nil {
				t.logger.Error("failed to set state", zap.Error(err))
				return
			}
		} else {
			t.logger.Error("failed to get state", zap.Error(err))
			return
		}
	}

	ctx := context.Background()

	if err := t.buyAndSellRandomly(ctx); err != nil {
		t.logger.Error("failed to run positions", zap.Error(err))
	}

	ticker := time.NewTicker(t.positionManageInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := t.buyAndSellRandomly(ctx); err != nil {
				t.logger.Error("failed to run positions", zap.Error(err))
			}
		}
	}
}

// unused for now
func (t *trader) positionsRun(ctx context.Context) error {
	if err := t.loadPositions(ctx); err != nil {
		return fmt.Errorf("failed to load positions: %w", err)
	}

	return t.iteratePlans(func(plan iroPlan) (bool, error) {
		// if position exists for the trader
		if pos, ok := t.positions[fmt.Sprint(plan.Id)]; ok {
			return false, t.manageExistingPosition(
				ctx, &plan, pos,
				plan.analyticsResp.Volume.oneDayChangeInPercent(),
				plan.analyticsResp.TotalSupply.oneDayPriceChangeInPercent(),
			)
		}

		return false, t.tryOpenNewPosition(ctx, &plan)
	})
}

// loadPositions gets all the balances for the trader account and loads all the plans,
// it then matches all the balances with plans by rollapp id and maps a position for each plan
func (t *trader) loadPositions(ctx context.Context) error {
	if err := t.accountSvc.refreshBalances(ctx); err != nil {
		return fmt.Errorf("failed to update balances: %w", err)
	}

	if err := t.updatePositions(); err != nil {
		return fmt.Errorf("failed to update plans: %w", err)
	}

	return nil
}

func (t *trader) updatePositions() error {
	// a position is a plan with a balance
	t.NAV = sdk.NewInt(0)

	if err := t.iteratePlans(func(plan iroPlan) (bool, error) {
		b := t.accountSvc.balanceOf(IRODenom(plan.RollappId))
		if b.IsPositive() {
			price := plan.SpotPrice()
			valueDYM := sdk.NewDecFromInt(b).Mul(price).RoundInt()
			t.positions[fmt.Sprint(plan.Id)] = position{
				valueDYM: valueDYM,
				amount:   b,
				isIRO:    true,
			}
			t.NAV = t.NAV.Add(valueDYM)
		}
		return false, nil
	}); err != nil {
		return fmt.Errorf("failed to iterate plans: %w", err)
	}

	if err := t.iterateRollapps(func(rollapp rollapp) (bool, error) {
		b := t.accountSvc.balanceOf(rollapp.IBCDenom)
		if b.IsPositive() {
			t.positions[rollapp.ChainID] = position{
				amount: sdk.NewInt(0),
			}
		}
		return false, nil
	}); err != nil {
		return fmt.Errorf("failed to iterate rollapps: %w", err)
	}

	return nil
}

/*
TODO:
  - how to get totalPortfolioValue ?
  - introduce configurable parameters for percentages for allocation
  - will previously closed positions be opened again???
*/
func (t *trader) tryOpenNewPosition(ctx context.Context, plan plan) error {
	// ===== decide how much to allocate based on the target raise of the IRO =====
	targetRaise := plan.TargetRaise()
	toAllocateTRPercent := sdk.NewDec(0)

	// If standard 0.05%
	if targetRaise.Equal(standardTargetRaise) {
		toAllocateTRPercent = sdk.MustNewDecFromStr("0.05")
		// if lower than standard 0.1%
	} else if targetRaise.LT(standardTargetRaise) {
		toAllocateTRPercent = sdk.MustNewDecFromStr("0.1")
		// if higher than standard 0.02%
	} else {
		toAllocateTRPercent = sdk.MustNewDecFromStr("0.02")
	}

	// ===== decide how much to allocate based on the amount of DYM bought before the bot got to it =====
	toAllocateSDPercent := sdk.NewDec(0)

	totalSoldDYM := plan.TotalSoldInDYM()
	// if nobody or less 1 DYM → buy 0.05% (from portfolio size)
	if totalSoldDYM.LT(sdk.NewInt(1)) {
		toAllocateSDPercent = sdk.MustNewDecFromStr("0.05")
		// if above 100 DYM  → buy 0.02%
	} else if totalSoldDYM.GT(sdk.NewInt(100)) {
		toAllocateSDPercent = sdk.MustNewDecFromStr("0.02")
	} else {
		// if between 1 - 100 DYM → buy 0.5%
		toAllocateSDPercent = sdk.MustNewDecFromStr("0.5")
	}

	toAllocatePercent := toAllocateTRPercent
	if toAllocateSDPercent.GT(toAllocateTRPercent) {
		toAllocatePercent = toAllocateSDPercent
	}
	totalPortfolioValue := sdk.NewDecFromInt(t.NAV).Add(sdk.NewDecFromInt(t.balanceDYM()))

	if err := t.ensureAccountIsPrimed(ctx, totalPortfolioValue); err != nil {
		return fmt.Errorf("failed to ensure account is primed: %w", err)
	}

	amountToAllocate := totalPortfolioValue.Mul(toAllocatePercent).RoundInt()
	tokenAmount := sdk.NewDecFromInt(amountToAllocate).Quo(plan.SpotPrice())

	t.logger.Info("opening new position", zap.Uint64("plan_id", plan.GetId()), zap.String("amount_to_allocate", amountToAllocate.String()), zap.String("token_amount", tokenAmount.String()))

	if err := t.buy(ctx, amountToAllocate, sdk.NewInt(1), fmt.Sprint(plan.GetId())); err != nil {
		return fmt.Errorf("failed to buy: %w", err)
	}

	t.positions[fmt.Sprint(plan.GetId())] = position{
		valueDYM:  amountToAllocate,
		amount:    tokenAmount.RoundInt(),
		createdAt: time.Now(),
	}

	st, err := t.getState()
	if err != nil {
		return fmt.Errorf("failed to get state: %w", err)
	}

	st.Positions[fmt.Sprint(plan.GetId())] = positionState{
		LastVolumeCheck: time.Now().Unix(),
		LastPriceCheck:  time.Now().Unix(),
	}

	if err := t.setState(st); err != nil {
		return fmt.Errorf("failed to set state: %w", err)
	}

	return nil
}

func (t *trader) ensureAccountIsPrimed(ctx context.Context, totalPortfolioValue sdk.Dec) error {
	// if the portfolio is empty, the allocated amount will be 0
	// so, we can prime the account with some DYM
	if !totalPortfolioValue.IsPositive() {
		t.logger.Info("account is empty, priming with 1DYM")
		amountToPrime := sdk.NewInt(1000000000000000000) // 1DYM
		ensuredDenoms, err := t.accountSvc.ensureBalances(ctx, sdk.NewCoins(sdk.NewCoin("adym", amountToPrime)))
		if err != nil {
			return fmt.Errorf("failed to ensure balances: %w", err)
		}

		if len(ensuredDenoms) == 0 {
			t.logger.Info("account not primed")
			return nil
		}
	}
	return nil
}

/*
TODO:
  - introduce configurable parameters for percentages
*/
func (t *trader) manageExistingPosition(
	ctx context.Context,
	plan plan,
	pos position,
	oneDayVolumeChangeInPercent, oneDayPriceChangeInPercent float64,
) error {
	planID := plan.GetId()
	valueInDYM := sdk.NewDecFromInt(pos.amount).Mul(plan.SpotPrice())
	balanceDym := sdk.NewDecFromInt(t.balanceDYM())
	nav := sdk.NewDecFromInt(t.NAV)
	percentage := valueInDYM.Quo(nav.Add(balanceDym))
	valInDYMStr := formatAmount(strings.Split(valueInDYM.String(), ".")[0])
	percParts := strings.Split(percentage.Mul(sdk.NewDec(100)).String(), ".")
	percentageStr := fmt.Sprintf("%s.%s", percParts[0], percParts[1][:4])

	t.logger.Debug("managing existing position",
		zap.Uint64("plan_id", planID),
		zap.String("value_in_dym", valInDYMStr),
		zap.String("percentage", fmt.Sprintf("%s", percentageStr)),
		zap.String("NAV", formatAmount(t.NAV.String())),
		zap.Float64("one_day_volume_change_in_percent", oneDayVolumeChangeInPercent),
		zap.Float64("one_day_price_change_in_percent", oneDayPriceChangeInPercent),
	)

	st, err := t.getState()
	if err != nil {
		return err
	}

	s := st.Positions[fmt.Sprint(planID)]
	now := time.Now().Unix()

	coolDownPeriod := getRandomCooldown(t.cooldownRangeMinutes[0], t.cooldownRangeMinutes[1]) // random
	nextVolumeCheck := s.LastVolumeCheck + int64(coolDownPeriod.Seconds())
	nextPriceCheck := s.LastPriceCheck + int64(coolDownPeriod.Seconds())
	canTradeVol := nextVolumeCheck <= now
	canTradePrice := nextPriceCheck <= now

	sellAmt := sdk.NewInt(0)

	// sell

	switch {
	// If a token's value increases beyond 10% of total portfolio value- > sell gradually excess to return to 10%.
	case percentage.GT(sdk.MustNewDecFromStr("0.1")):
		t.logger.Info("token's value increased beyond 10% of total portfolio value, selling gradually to return to 10%",
			zap.Uint64("plan_id", planID),
			zap.String("percentage", fmt.Sprintf("%s", percentage.Mul(sdk.NewDec(100)).String())))
		// get the amount to sell (every time 2%)
		sellAmt = sdk.NewDecFromInt(pos.amount).Mul(sdk.MustNewDecFromStr("0.02")).RoundInt()
	// If a token's value drops below 0.05% of total portfolio value sell the entire position.
	case percentage.LT(sdk.MustNewDecFromStr("0.0005")):
		t.logger.Info("token's value dropped below 0.05% of total portfolio value, selling entire position",
			zap.Uint64("plan_id", planID),
			zap.String("percentage", fmt.Sprintf("%s", percentage.Mul(sdk.NewDec(100)).String())))

		balances, err := t.getBalances(ctx)
		if err != nil {
			return fmt.Errorf("failed to get account balances: %w", err)
		}

		sellAmt = balances.AmountOf(IRODenom(plan.GetRollappId()))
		delete(t.positions, fmt.Sprint(planID))
	// If volume decreases >50%, decrease position by 10% of current size.
	case canTradeVol && oneDayVolumeChangeInPercent <= -50:
		t.logger.Info("volume decreased >50%, decreasing position by 10%",
			zap.Uint64("plan_id", planID),
			zap.Float64("one_day_change_in_percent", oneDayVolumeChangeInPercent))
		sellAmt = pos.amount.Quo(sdk.NewInt(10))
		s.LastVolumeCheck = now
	// Implement a trailing stop-loss of 50% for each token position.

	// if token dropped in 25% price → sell 50%.
	case canTradePrice && oneDayPriceChangeInPercent <= -25:
		t.logger.Info("token dropped in 25% price, selling 50% of position",
			zap.Uint64("plan_id", planID),
			zap.Float64("one_day_price_change_in_percent", oneDayPriceChangeInPercent))
		// get the amount to sell (every time 50%)
		sellAmt = pos.amount.Mul(sdk.NewInt(50)).Quo(sdk.NewInt(100))
		s.LastPriceCheck = now
	// If price decreases >10%, decrease position by 10% of current size.
	case canTradePrice && oneDayPriceChangeInPercent <= -10:
		t.logger.Info("price decreases >10%, decreasing position by 10%",
			zap.Uint64("plan_id", planID),
			zap.Float64("one_day_price_change_in_percent", oneDayPriceChangeInPercent))
		// get the amount to sell (every time 10%)
		sellAmt = pos.amount.Quo(sdk.NewInt(10))
		s.LastPriceCheck = now
	// Take profits on individual tokens that have gained > 200% by selling 25% the position.
	case canTradePrice && oneDayPriceChangeInPercent > 200:
		t.logger.Info("token have gained > 200%, selling 25% of position",
			zap.Uint64("plan_id", planID),
			zap.Float64("one_day_price_change_in_percent", oneDayPriceChangeInPercent))
		sellAmt = pos.amount.Mul(sdk.NewInt(25)).Quo(sdk.NewInt(100))
		s.LastPriceCheck = now
	}

	if sellAmt.GT(sdk.ZeroInt()) {
		if err := t.sell(ctx, sellAmt, plan.MinIncome(sellAmt), fmt.Sprint(planID)); err != nil {
			return fmt.Errorf("failed to sell: planID %d; %w", planID, err)
		}

		st.Positions[fmt.Sprint(planID)] = s
		if err := t.setState(st); err != nil {
			return fmt.Errorf("failed to set state: %w", err)
		}
		return nil
	}

	// Only execute (buy) trade if (Current DYM reserves / Total portfolio value) > 0.2
	dymReserveToTotalPortfolio := balanceDym.Quo(nav)
	if !dymReserveToTotalPortfolio.GT(sdk.MustNewDecFromStr("0.2")) {
		return nil
	}

	// buy

	buyAmt := sdk.NewDec(0)

	switch {
	// If volume increases >50% on a daily normalized timeframe, increase position by 10% of current size.
	case canTradeVol && oneDayVolumeChangeInPercent > 50:
		t.logger.Info("volume increased >50%, increasing position by 10%",
			zap.Uint64("plan_id", planID),
			zap.Float64("one_day_change_in_percent", oneDayVolumeChangeInPercent))
		// get the amount to buy (every time 10%)
		buyAmt = sdk.NewDecFromInt(pos.amount.Quo(sdk.NewInt(10)))
		s.LastVolumeCheck = now
	// If price increases >10% on a daily normalized timeframe, increase position by 10% of current size.
	case canTradePrice && oneDayPriceChangeInPercent > 10:
		t.logger.Info("price increased >10%, increasing position by 10%",
			zap.Uint64("plan_id", planID),
			zap.Float64("one_day_price_change_in_percent", oneDayPriceChangeInPercent))
		// get the amount to buy (every time 10%)
		buyAmt = sdk.NewDecFromInt(pos.amount.Quo(sdk.NewInt(10)))
		s.LastPriceCheck = now
	}

	if !buyAmt.IsPositive() {
		return nil
	}

	spend := buyAmt.Mul(plan.SpotPrice()).RoundInt()
	minAmount, err := plan.MinAmount(spend)
	if err != nil {
		return fmt.Errorf("failed to get min amount: %w", err)
	}

	// buy
	if err = t.buy(ctx, spend, minAmount, fmt.Sprint(planID)); err != nil {
		return fmt.Errorf("failed to buy: planID %d; %w", planID, err)
	}

	st.Positions[fmt.Sprint(planID)] = s
	if err := t.setState(st); err != nil {
		return fmt.Errorf("failed to set state: %w", err)
	}

	return nil
}

func (t *trader) buyAndSellRandomly(ctx context.Context) error {
	if err := t.loadPositions(ctx); err != nil {
		return fmt.Errorf("failed to load positions: %w", err)
	}

	st, err := t.getState()
	if err != nil {
		return err
	}

	positionPlanIDs := make([]string, 0, len(t.positions))
	positionRollappIDs := make([]string, 0, len(t.positions))
	for id, p := range t.positions {
		if p.isIRO {
			positionPlanIDs = append(positionPlanIDs, fmt.Sprint(id))
		} else {
			positionRollappIDs = append(positionRollappIDs, fmt.Sprint(id))
		}
	}

	slices.Sort(positionPlanIDs)

	t.logger.Info("open positions",
		zap.Time("next_trade", time.Unix(st.NextTrade, 0)),
		zap.Strings("plan_ids", positionPlanIDs),
		zap.Strings("rollapp_ids", positionRollappIDs))

	now := time.Now()
	nowU := now.Unix()
	bedtimeEndHour := (t.bedtimeStartHour + defaultBedtimeDurationHours) % 24

	if now.Hour() >= t.bedtimeStartHour && now.Hour() < bedtimeEndHour {
		t.logger.Info("bedtime, no trading")
		//	return nil
	}

	if st.NextTrade > nowU {
		//	return nil
	}

	st.LastTrade = nowU
	coolDownPeriod := getRandomCooldown(t.cooldownRangeMinutes[0], t.cooldownRangeMinutes[1])
	st.NextTrade = nowU + int64(coolDownPeriod.Seconds())

	canOpen := len(t.positions) < t.maxPositions
	if t.positions == nil {
		t.logger.Info("positions is nil")
	}

	if planOrRollapp := rand.Intn(2); planOrRollapp == 0 {
		pl := t.getRandomPlan(t.maxPositions)
		if pl == nil {
			t.logger.Info("no plans to trade")
		}

		if err := t.tradePlan(ctx, pl, canOpen); err != nil {
			return fmt.Errorf("failed to trade plan: %w", err)
		}
	} else {
		r := t.getRandomRollapp(t.maxPositions)
		if r == nil {
			t.logger.Info("no rollapps to trade")
		}
		if err := t.tradeRollapp(ctx, r, canOpen); err != nil {
			return fmt.Errorf("failed to trade rollapp: %w", err)
		}
	}

	if err := t.setState(st); err != nil {
		return fmt.Errorf("failed to set state: %w", err)
	}

	return nil
}

func (t *trader) tradePlan(ctx context.Context, pl *iroPlan, canOpen bool) error {
	// if position exists for the trader
	if _, ok := t.positions[fmt.Sprint(pl.Id)]; ok {
		if err := t.manageRandomPlanPosition(ctx, pl); err != nil {
			return fmt.Errorf("failed to manage random position: %w", err)
		}

		return nil
	}

	if canOpen {
		if err := t.openRandomPlanPosition(ctx, pl); err != nil {
			return fmt.Errorf("failed to open random position: %w", err)
		}
	}

	return nil
}

func (t *trader) tradeRollapp(ctx context.Context, r *rollapp, canOpen bool) error {
	// if position exists for the trader
	if _, ok := t.positions[r.ChainID]; ok {
		if err := t.manageRandomRollappPosition(ctx, r); err != nil {
			return fmt.Errorf("failed to manage random position: %w", err)
		}

		return nil
	}

	if canOpen {
		if err := t.openRandomRollappPosition(ctx, r); err != nil {
			return fmt.Errorf("failed to open random position: %w", err)
		}
	}

	return nil
}

func (t *trader) openRandomPlanPosition(ctx context.Context, plan *iroPlan) error {
	// get a random amount to buy from 5 to 50 DYM
	amount := sdk.NewInt(int64(rand.Intn(maxDYMForTrade) + minDYMForTrade)).Mul(DYM) // from 5 to 50 DYM
	minAmount, err := plan.MinAmount(amount)
	if err != nil {
		return fmt.Errorf("failed to get min amount: %w", err)
	}

	if err := t.buy(ctx, amount, minAmount, fmt.Sprint(plan.GetId())); err != nil {
		return fmt.Errorf("failed to buy: %w", err)
	}

	t.positions[fmt.Sprint(plan.GetId())] = position{
		valueDYM:  amount,
		amount:    minAmount,
		createdAt: time.Now(),
	}

	return nil
}

func (t *trader) manageRandomPlanPosition(
	ctx context.Context,
	plan *iroPlan,
) error {
	// buy or sell random amount
	if rand.Intn(2) == 0 {
		// buy
		amount := sdk.NewInt(int64(rand.Intn(maxDYMForTrade) + minDYMForTrade)).Mul(DYM) // from 5 to 50 DYM
		minAmount, err := plan.MinAmount(amount)
		if err != nil {
			return fmt.Errorf("failed to get min amount: %w", err)
		}

		if err := t.buy(ctx, amount, minAmount, fmt.Sprint(plan.GetId())); err != nil {
			return fmt.Errorf("failed to buy: %w", err)
		}
	} else {
		// sell
		balances, err := t.getBalances(ctx)
		if err != nil {
			return fmt.Errorf("failed to get account balances: %w", err)
		}

		totalAmount := balances.AmountOf(IRODenom(plan.GetRollappId()))
		sellAmtPercent := rand.Intn(96) + 5 // from 5 to 100%
		sellAmt := totalAmount.Mul(sdk.NewInt(int64(sellAmtPercent))).Quo(sdk.NewInt(100))

		if totalAmount.IsZero() {
			delete(t.positions, fmt.Sprint(plan.Id))
			return nil
		}

		if err := t.sell(ctx, sellAmt, plan.MinIncome(sellAmt), fmt.Sprint(plan.GetId())); err != nil {
			return fmt.Errorf("failed to sell: %w", err)
		}

		if sellAmtPercent == 100 {
			delete(t.positions, fmt.Sprint(plan.Id))
		}
	}

	return nil
}

func (t *trader) buyAmount(ctx context.Context, spend, minAmount sdk.Int, planID string) error {
	toppedUp, err := t.accountSvc.ensureBalances(ctx, sdk.NewCoins(sdk.NewCoin("adym", spend)))
	if err != nil {
		return fmt.Errorf("failed to ensure balances: %w", err)
	}

	if len(toppedUp) == 0 {
		t.logger.Info("balances not topped up")
		return nil
	}

	buyMsg := &types.MsgBuyExactSpend{
		Buyer:              t.accountSvc.address(),
		PlanId:             planID,
		Spend:              spend,
		MinOutTokensAmount: minAmount,
	}

	tx, err := t.client.BroadcastTx(t.accountSvc.accountName, buyMsg)
	if err != nil {
		return fmt.Errorf("failed to broadcast tx: %w", err)
	}

	if _, err := waitForTx(t.accountSvc.client, tx.TxHash); err != nil {
		return fmt.Errorf("failed to wait for tx: %w", err)
	}

	t.logger.Info("bought", zap.String("spend", spend.String()), zap.String("min_amount", minAmount.String()))

	// refresh balance
	if err := t.accountSvc.refreshBalances(ctx); err != nil {
		return fmt.Errorf("failed to update balances: %w", err)
	}

	return nil
}

func (t *trader) sellAmount(ctx context.Context, amount, minIncome sdk.Int, planID string) error {
	// only ensure gas
	if _, err := t.accountSvc.ensureBalances(ctx, sdk.NewCoins()); err != nil {
		return fmt.Errorf("failed to ensure balances: %w", err)
	}

	sellMsg := &types.MsgSell{
		Seller:          t.accountSvc.address(),
		PlanId:          planID,
		Amount:          amount,
		MinIncomeAmount: minIncome,
	}

	tx, err := t.client.BroadcastTx(t.accountSvc.accountName, sellMsg)
	if err != nil {
		return fmt.Errorf("failed to broadcast tx: %w", err)
	}

	if _, err := waitForTx(t.accountSvc.client, tx.TxHash); err != nil {
		return fmt.Errorf("failed to wait for tx: %w", err)
	}

	t.logger.Info("sold", zap.String("amount", amount.String()), zap.String("min_income", minIncome.String()))

	// refresh balance
	if err := t.accountSvc.refreshBalances(ctx); err != nil {
		return fmt.Errorf("failed to update balances: %w", err)
	}

	return nil
}

func (t *trader) openRandomRollappPosition(ctx context.Context, rollapp *rollapp) error {
	// get a random amount to buy from 5 to 50 DYM
	amount := sdk.NewInt(int64(rand.Intn(maxDYMForTrade-minDYMForTrade+1) + minDYMForTrade)).Mul(DYM) // from 5 to 50 DYM
	coin := sdk.NewCoin("adym", amount)
	minAmount := sdk.NewInt(1) // TODO

	toppedUp, err := t.accountSvc.ensureBalances(ctx, sdk.NewCoins(coin))
	if err != nil {
		return fmt.Errorf("failed to ensure balances: %w", err)
	}

	if len(toppedUp) == 0 {
		t.logger.Info("balances not topped up")
		return nil
	}

	if err := t.swapAmount(ctx, coin, minAmount, rollapp.PoolID, rollapp.IBCDenom); err != nil {
		return fmt.Errorf("failed to buy: %w", err)
	}

	t.positions[rollapp.ChainID] = position{
		valueDYM:  amount,
		amount:    sdk.NewInt(0),
		createdAt: time.Now(),
	}

	return nil
}

func (t *trader) manageRandomRollappPosition(
	ctx context.Context,
	rollapp *rollapp,
) error {
	// buy or sell random amount
	if rand.Intn(2) == 0 {
		// buy
		amount := sdk.NewInt(int64(rand.Intn(maxDYMForTrade-minDYMForTrade+1) + minDYMForTrade)).Mul(DYM) // from 5 to 50 DYM
		coin := sdk.NewCoin("adym", amount)

		toppedUp, err := t.accountSvc.ensureBalances(ctx, sdk.NewCoins(coin))
		if err != nil {
			return fmt.Errorf("failed to ensure balances: %w", err)
		}

		if len(toppedUp) == 0 {
			t.logger.Info("balances not topped up")
			return nil
		}

		minAmount := sdk.NewInt(1) // TODO

		if err := t.swapAmount(ctx, coin, minAmount, rollapp.PoolID, rollapp.IBCDenom); err != nil {
			return fmt.Errorf("failed to buy: %w", err)
		}
	} else {
		// sell

		balances, err := t.getBalances(ctx)
		if err != nil {
			return fmt.Errorf("failed to get account balances: %w", err)
		}

		totalAmount := balances.AmountOf(rollapp.IBCDenom)
		sellAmtPercent := rand.Intn(96) + 5 // from 5 to 100%
		sellAmt := totalAmount.Mul(sdk.NewInt(int64(sellAmtPercent))).Quo(sdk.NewInt(100))

		if totalAmount.IsZero() {
			delete(t.positions, rollapp.ChainID)
			return nil
		}

		// only ensure gas
		if _, err := t.accountSvc.ensureBalances(ctx, sdk.NewCoins()); err != nil {
			return fmt.Errorf("failed to ensure balances: %w", err)
		}

		minAmount := sdk.NewInt(1) // TODO
		if err := t.swapAmount(ctx, sdk.NewCoin(rollapp.IBCDenom, sellAmt), minAmount, rollapp.PoolID, "adym"); err != nil {
			return fmt.Errorf("failed to sell: %w", err)
		}

		if sellAmtPercent == 100 {
			delete(t.positions, rollapp.ChainID)
		}
	}

	return nil
}

func (t *trader) swapAmount(ctx context.Context, spend sdk.Coin, minAmount sdk.Int, poolId uint64, outDenom string) error {
	swapMsg := &types.MsgSwapExactAmountIn{
		Sender: t.accountSvc.address(),
		Routes: []types.SwapAmountInRoute{{
			PoolId:        poolId,
			TokenOutDenom: outDenom,
		}},
		TokenIn:           spend,
		TokenOutMinAmount: minAmount,
	}

	t.logger.Debug("swapping", zap.String("spend", spend.String()), zap.String("min_amount", minAmount.String()), zap.String("out_denom", outDenom))

	tx, err := t.client.BroadcastTx(t.accountSvc.accountName, swapMsg)
	if err != nil {
		if strings.Contains(err.Error(), "invalid calculated result") {
			t.removeGammPool(outDenom) // TODO: skip rollapp until liquidity provided
		}
		return fmt.Errorf("failed to broadcast tx: %w", err)
	}

	txRes, err := waitForTx(t.accountSvc.client, tx.TxHash)
	if err != nil {
		return fmt.Errorf("failed to wait for tx: %w", err)
	}

	var tokensOut string

	for _, ev := range txRes.TxResponse.Events {
		if ev.Type == "token_swapped" {
			for _, attr := range ev.Attributes {
				if string(attr.Key) == "tokens_out" {
					tOut, err := sdk.ParseCoinNormalized(string(attr.Value))
					if err != nil {
						t.logger.Error("failed to parse tokens out", zap.Error(err))
					}
					tokensOut = tOut.String()
				}
			}
		}
	}

	t.logger.Info("swapped", zap.String("spend", spend.String()), zap.String("out", tokensOut))

	// refresh balance
	if err := t.accountSvc.refreshBalances(ctx); err != nil {
		return fmt.Errorf("failed to update balances: %w", err)
	}

	return nil
}

func getRandomCooldown(min, max int) time.Duration {
	delta := max - min
	if delta < 0 {
		delta = 0
	}
	r := rand.Intn(delta+1) + min
	return time.Duration(r) * time.Minute
}

func (t *trader) balanceOfDYM() sdk.Int {
	return t.accountSvc.balanceOf("adym")
}

func formatAmount(numStr string) string {
	if len(numStr) <= 18 {
		return "0." + string(strings.Repeat("0", 18-len(numStr)) + numStr)[:4]
	}
	return numStr[:len(numStr)-18] + "." + numStr[len(numStr)-18:len(numStr)-14]
}
