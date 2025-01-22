package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/dymensionxyz/eco-bot/types"
)

type querier struct {
	client       cosmosClient
	httpClient   http.Client
	chainID      string
	analyticsURL string
}

func newQuerier(client cosmosClient, analyticsURL string) querier {
	return querier{
		client:       client,
		httpClient:   http.Client{},
		chainID:      client.Context().ChainID,
		analyticsURL: analyticsURL,
	}
}

func (q querier) queryIROPlan(ctx context.Context, id string) (*types.Plan, error) {
	c := types.NewQueryClient(q.client.Context())
	resp, err := c.QueryPlan(ctx, &types.QueryPlanRequest{
		PlanId: id,
	})
	if err != nil {
		return nil, fmt.Errorf("query plan: %w", err)
	}
	return resp.Plan, nil
}

// TODO: use analytics or indexer API instead of RPC
func (q querier) queryIROPlans(ctx context.Context) ([]types.Plan, error) {
	c := types.NewQueryClient(q.client.Context())
	resp, err := c.QueryPlans(ctx, &types.QueryPlansRequest{
		NonSettledOnly: true,
		Pagination:     nil, // TODO: pagination
	})
	if err != nil {
		return nil, fmt.Errorf("query plans: %w", err)
	}

	now := time.Now()

	var plans []types.Plan
	for _, p := range resp.Plans {
		if p.SettledDenom != "" {
			continue
		}
		if p.StartTime.After(now) {
			continue
		}
		plans = append(plans, p)
	}

	slices.SortFunc(plans, func(p1, p2 types.Plan) int {
		if p1.Id > p2.Id {
			return 1
		}
		if p1.Id < p2.Id {
			return -1
		}
		return 0
	})

	return plans, nil
}

func (q querier) queryTokensForDYM(ctx context.Context, planID string, amt sdk.Int) (*sdk.Coin, error) {
	c := types.NewQueryClient(q.client.Context())
	resp, err := c.QueryTokensForDYM(ctx, &types.QueryTokensForDYMRequest{
		PlanId: planID,
		Amt:    amt,
	})
	if err != nil {
		return nil, fmt.Errorf("query tokens for DYM: %w", err)
	}
	return resp.Tokens, nil
}

func (q querier) querySpotPrice(ctx context.Context, planID string) (sdk.Dec, error) {
	c := types.NewQueryClient(q.client.Context())

	resp, err := c.QuerySpotPrice(ctx, &types.QuerySpotPriceRequest{
		PlanId: planID,
	})
	if err != nil {
		return sdk.Dec{}, fmt.Errorf("query spot price: %w", err)
	}
	return resp.Price, nil
}

func (q querier) queryBalances(ctx context.Context, address string) (sdk.Coins, error) {
	c := banktypes.NewQueryClient(q.client.Context())

	resp, err := c.SpendableBalances(ctx, &banktypes.QuerySpendableBalancesRequest{
		Address: address,
	})
	if err != nil {
		return nil, fmt.Errorf("query balances: %w", err)
	}
	return resp.Balances, nil
}

func (q querier) queryAnalytics(rollappID string) (*analyticsResp, error) {
	url := fmt.Sprintf("%s?networkId=%s&dataType=rollapps&itemId=%s", q.analyticsURL, q.chainID, rollappID)
	resp, err := get[analyticsResp](q.httpClient, url)
	if err != nil {
		return nil, fmt.Errorf("query analytics: %w", err)
	}
	return &resp, nil
}

func get[T any](client http.Client, url string) (ret T, err error) {
	resp, err := client.Get(url)
	if err == nil && resp.StatusCode == http.StatusOK {
		err = json.NewDecoder(resp.Body).Decode(&ret)
	}
	return
}
