package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/cosmos/cosmos-sdk/x/authz"
	"github.com/dymensionxyz/cosmosclient/cosmosclient"
	"go.uber.org/zap"

	"github.com/dymensionxyz/eco-bot/types"
)

type trader struct {
	account               account
	client                cosmosClient
	balancesIRORollappMap map[string]sdk.Int
	balanceDYM            sdk.Int
	bmu                   sync.Mutex
	NAV                   sdk.Int // net asset value of all positions in DYM
	q                     querier
	iteratePlans          func(func(types.Plan) error) error
	positions             map[uint64]position
	policyAddress         string
	operatorAddress       string
	logger                *zap.Logger
}

type position struct {
	valueDYM  sdk.Int
	amount    sdk.Int
	createdAt time.Time
}

type cosmosClient interface {
	BroadcastTx(accountName string, msgs ...sdk.Msg) (cosmosclient.Response, error)
	Context() client.Context
}

var (
	DYM = sdk.NewIntFromBigInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
)

func newTrader(
	acc account,
	q querier,
	iteratePlans func(func(types.Plan) error) error,
	policyAddress,
	operatorAddress string,
	cClient cosmosClient,
	logger *zap.Logger,
) *trader {
	return &trader{
		client:                cClient,
		q:                     q,
		iteratePlans:          iteratePlans,
		account:               acc,
		positions:             make(map[uint64]position),
		balancesIRORollappMap: make(map[string]sdk.Int),
		policyAddress:         policyAddress,
		operatorAddress:       operatorAddress,
		logger:                logger,
	}
}

// loadPositions gets all the balances for the trader account and loads all the plans,
// it then matches all the balances with plans by rollapp id and maps a position for each plan
func (t *trader) loadPositions(ctx context.Context) error {
	if err := t.updateBalances(ctx); err != nil {
		return fmt.Errorf("failed to update balances: %w", err)
	}

	if err := t.updatePlans(ctx); err != nil {
		return fmt.Errorf("failed to update plans: %w", err)
	}

	return nil
}

func (t *trader) updateBalances(ctx context.Context) error {
	balances, err := t.q.queryBalances(ctx, t.account.Address)
	if err != nil {
		return fmt.Errorf("failed to query balances: %w", err)
	}

	t.bmu.Lock()
	defer t.bmu.Unlock()

	for _, balance := range balances {
		if !strings.HasPrefix(balance.Denom, IROTokenPrefix) {
			if balance.Denom == "adym" {
				t.balanceDYM = balance.Amount
			}
			continue
		}
		rollappID := strings.TrimPrefix(balance.Denom, IROTokenPrefix)
		t.balancesIRORollappMap[rollappID] = balance.Amount
	}

	return nil
}

func (t *trader) updatePlans(ctx context.Context) error {
	// get plans
	plans, err := t.q.queryIROPlans(ctx)
	if err != nil {
		return fmt.Errorf("failed to query plans: %w", err)
	}

	// position is a plan with a balance
	for _, plan := range plans {
		if b, ok := t.balancesIRORollappMap[plan.RollappId]; ok {
			price := plan.SpotPrice()
			valueDYM := sdk.NewDecFromInt(b).Mul(price).RoundInt()
			t.positions[plan.Id] = position{
				valueDYM: valueDYM,
				amount:   b,
			}
			t.NAV = t.NAV.Add(valueDYM)
		}
	}

	return nil
}

// managePositions periodically traverses all the positions and applies logic to manage them by trading.
// logic is applied to decide what to do with current positions, and if any new positions should be opened.
func (t *trader) managePositions() {
	ctx := context.Background()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := t.loadPositions(ctx); err != nil {
				t.logger.Error("failed to load positions", zap.Error(err))
				continue
			}

			err := t.iteratePlans(func(plan types.Plan) error {
				analytics, err := t.q.queryAnalytics(plan.RollappId)
				if err != nil {
					return fmt.Errorf("failed to query analytics: %w", err)
				}

				_ = analytics

				// if position exists for the trader
				if pos, ok := t.positions[plan.Id]; ok {
					return t.manageExistingPosition(plan.Id, ctx, pos)
				}

				return t.tryOpenNewPosition(plan.Id, ctx)
			})
			if err != nil {
				t.logger.Error("failed to iterate plans", zap.Error(err))
				continue
			}
		}
	}
}

func (t *trader) tryOpenNewPosition(planID uint64, ctx context.Context) error {
	// if position doesn't exist, open a new one
	// get the amount of DYM to spend
	tokens, err := t.q.queryTokensForDYM(ctx, fmt.Sprint(planID), sdk.NewInt(1))
	if err != nil {
		t.logger.Error("failed to query tokens for DYM", zap.Error(err))
		return nil
	}

	_ = tokens

	return nil
}

func (t *trader) manageExistingPosition(planID uint64, ctx context.Context, pos position) error {
	plan, err := t.q.queryIROPlan(ctx, fmt.Sprint(planID))
	if err != nil {
		t.logger.Error("failed to get plan", zap.Error(err))
		return err
	}

	// buy or sell

	valueInDYM := sdk.NewDecFromInt(pos.amount).Mul(plan.SpotPrice())
	percentage := valueInDYM.Quo(sdk.NewDecFromInt(t.NAV))

	// If a token's value increases beyond 10% of total portfolio value- > sell gradually excess to return to 10%.
	if percentage.GT(sdk.MustNewDecFromStr("0.1")) {
		// get the amount to sell (every time 1%)
		amt := valueInDYM.Mul(sdk.MustNewDecFromStr("0.01")).Quo(plan.SpotPrice()).RoundInt()

		// get the min income
		minIncome := amt.Quo(sdk.NewInt(5)) // make min income half the amount

		// sell
		if err = t.sellAuthorized(amt, minIncome, fmt.Sprint(planID)); err != nil {
			t.logger.Error("failed to sell", zap.Error(err))
			return err
		}

		return nil
	}

	// If a token's value drops below 0.05% of total portfolio value sell the entire position.
	if percentage.LT(sdk.MustNewDecFromStr("0.0005")) {
		// sell the entire position
		if err = t.sellAuthorized(pos.amount, pos.valueDYM, fmt.Sprint(planID)); err != nil {
			t.logger.Error("failed to sell", zap.Error(err))
			return err
		}

		return nil
	}

	// TODO: check volume change and buy/sell accordingly

	return nil
}

func (t *trader) buyAuthorized(spend, minAmount sdk.Int, planID string) error {
	buyMsg := &types.MsgBuyExactSpend{
		Buyer:              t.operatorAddress,
		PlanId:             planID,
		Spend:              spend,
		MinOutTokensAmount: minAmount,
	}

	return t.execAuthorized(buyMsg)
}

func (t *trader) sellAuthorized(amount, minIncome sdk.Int, planID string) error {
	sellMsg := &types.MsgSell{
		Seller:          t.operatorAddress,
		PlanId:          planID,
		Amount:          amount,
		MinIncomeAmount: minIncome,
	}

	return t.execAuthorized(sellMsg)
}

func (t *trader) execAuthorized(msg sdk.Msg) error {
	// bech32 decode the policy address
	_, policyAddress, err := bech32.DecodeAndConvert(t.policyAddress)
	if err != nil {
		return fmt.Errorf("failed to decode policy address: %w", err)
	}

	authzMsg := authz.NewMsgExec(policyAddress, []sdk.Msg{msg})

	proposalMsg, err := types.NewMsgSubmitProposal(
		t.policyAddress,
		[]string{t.account.Address},
		[]sdk.Msg{&authzMsg},
		"== Fulfill Order ==",
		types.Exec_EXEC_TRY,
		"trade-iro-authorized",
		"trade-iro-authorized",
	)
	if err != nil {
		return fmt.Errorf("failed to create proposal message: %w", err)
	}

	rsp, err := t.client.BroadcastTx(t.account.Name, proposalMsg)
	if err != nil {
		return fmt.Errorf("failed to broadcast tx: %w", err)
	}

	// t.logger.Info("broadcast tx", zap.String("tx-hash", rsp.TxHash))

	resp, err := waitForTx(t.client, rsp.TxHash)
	if err != nil {
		return fmt.Errorf("failed to wait for tx: %w", err)
	}

	var presp []proposalResp
	if err = json.Unmarshal([]byte(resp.TxResponse.RawLog), &presp); err != nil {
		return fmt.Errorf("failed to unmarshal tx response: %w", err)
	}

	// hack to extract error from logs
	for _, p := range presp {
		for _, ev := range p.Events {
			if ev.Type == "cosmos.group.v1.EventExec" {
				for _, attr := range ev.Attributes {
					if attr.Key == "logs" && strings.Contains(attr.Value, "proposal execution failed") {
						theErr := ""
						parts := strings.Split(attr.Value, " : ")
						if len(parts) > 1 {
							theErr = parts[1]
						} else {
							theErr = attr.Value
						}
						return fmt.Errorf("proposal execution failed: %s", theErr)
					}
				}
			}
		}
	}

	t.logger.Info("tx executed", zap.String("tx-hash", rsp.TxHash))

	return nil
}

type proposalResp struct {
	MsgIndex int `json:"msg_index"`
	Events   []struct {
		Type       string `json:"type"`
		Attributes []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"attributes"`
	} `json:"events"`
}
