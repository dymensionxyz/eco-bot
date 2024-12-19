package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/dymensionxyz/eco-bot/types"
)

type querier struct {
	client     cosmosClient
	httpClient http.Client
	chainID    string
}

func newQuerier(client cosmosClient) querier {
	return querier{
		client:     client,
		httpClient: http.Client{},
		chainID:    "dymension_1405-1",
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

// TODO: pagination
func (q querier) queryIROPlans(ctx context.Context) ([]types.Plan, error) {
	c := types.NewQueryClient(q.client.Context())
	resp, err := c.QueryPlans(ctx, &types.QueryPlansRequest{
		TradableOnly: true,
	})
	if err != nil {
		return nil, fmt.Errorf("query plans: %w", err)
	}

	var plans []types.Plan
	for _, p := range resp.Plans {
		if p.SettledDenom != "" {
			continue
		}
		plans = append(plans, p)
	}

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

// TODO: from config
const analyticsURL = "https://fetchnetworkdataitemrequest-zbrmx4rjia-uc.a.run.app/?networkId=%s&dataType=rollapps&itemId=%s"

func (q querier) queryAnalytics(rollappID string) (*analyticsResp, error) {
	url := fmt.Sprintf(analyticsURL, q.chainID, rollappID)
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
