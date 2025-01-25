package bot

import (
	"encoding/json"
	"fmt"
	"os"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/dymensionxyz/eco-bot/types"
)

func tokensForDYM(plan *types.Plan, amt sdk.Int) (sdk.Coin, error) {
	tokensAmt, err := plan.BondingCurve.TokensForExactDYM(plan.SoldAmt, amt)
	if err != nil {
		return sdk.Coin{}, err
	}

	return sdk.NewCoin(IRODenom(plan.RollappId), tokensAmt), nil
}

type account struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type state struct {
	Positions map[string]positionState `json:"positions"`
}

type positionState struct {
	LastVolumeCheck int64 `json:"lastVolumeCheck"`
	LastPriceCheck  int64 `json:"lastPriceCheck"`
}

func newStateKeeper(path string) (func(*state) error, func() (*state, error)) {
	return func(s *state) error {
			jsn, err := json.Marshal(s)
			if err != nil {
				return err
			}
			return os.WriteFile(path, jsn, 0644)
		},
		func() (*state, error) {
			jsn, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			var s state
			if err := json.Unmarshal(jsn, &s); err != nil {

				return nil, err
			}
			return &s, nil
		}
}

const IROTokenPrefix = "IRO/"

func IRODenom(rollappID string) string {
	return fmt.Sprintf("%s%s", IROTokenPrefix, rollappID)
}

type analyticsResp struct {
	Volume volume `json:"volume"`
	// CreationDate   int64          `json:"creationDate"`
	// Currencies     []currency     `json:"currencies"`
	// Bech32Prefix   string         `json:"bech32Prefix"`
	// Launched       bool           `json:"launched"`
	// Type           string         `json:"type"`
	// CoinType       int            `json:"coinType"`
	// PreLaunchTime  int64          `json:"preLaunchTime"`
	// InitialSupply  float64        `json:"initialSupply"`
	// Status         string         `json:"status"`
	TotalSupply totalSupply `json:"totalSupply"`
	// TotalSponsored totalSponsored `json:"totalSponsored"`
}

type volume struct {
	// DiffWeek              float64 `json:"diffWeek"`
	// PreviousWeekValue     float64 `json:"previousWeekValue"`
	// PreviousTwoDaysValue  float64 `json:"previousTwoDaysValue"`
	// PreviousTwoWeeksValue float64 `json:"previousTwoWeeksValue"`
	PreviousDayValue float64 `json:"previousDayValue"`
	Value            float64 `json:"value"`
	// DiffDay               float64 `json:"diffDay"`
}

func (v volume) oneDayChangeInPercent() float64 {
	if v.PreviousDayValue == 0 {
		return 0
	}
	return ((v.Value - v.PreviousDayValue) / v.PreviousDayValue) * 100
}

type currency struct {
	DisplayDenom string `json:"displayDenom"`
	BaseDenom    string `json:"baseDenom"`
	Decimals     int    `json:"decimals"`
	Type         string `json:"type"`
}

type totalSupply struct {
	// PreviousTwoWeeksValue value   `json:"previousTwoWeeksValue"`
	// DiffDay               float64 `json:"diffDay"`
	// DiffWeek              float64 `json:"diffWeek"`
	Value value `json:"value"`
	// PreviousTwoDaysValue  value   `json:"previousTwoDaysValue"`
	PreviousDayValue value `json:"previousDayValue"`
	// PreviousWeekValue     value   `json:"previousWeekValue"`
}

func (t totalSupply) price() float64 {
	if t.Value.Amount == 0 {
		return 0
	}
	// marketCap is 1e6-based
	// totalSupply is 1e18-based
	// => multiply marketCap by 1e12 so they share the same exponent:
	return (float64(t.Value.MarketCap) * 1e12) / t.Value.Amount
}

func (t totalSupply) previousDayPrice() float64 {
	if t.PreviousDayValue.Amount == 0 {
		return 0
	}
	// marketCap is 1e6-based
	// totalSupply is 1e18-based
	// => multiply marketCap by 1e12 so they share the same exponent:
	return (float64(t.PreviousDayValue.MarketCap) * 1e12) / t.PreviousDayValue.Amount
}

func (t totalSupply) oneDayPriceChangeInPercent() float64 {
	if t.previousDayPrice() == 0 {
		return 0
	}
	return ((t.price() - t.previousDayPrice()) / t.previousDayPrice()) * 100
}

type value struct {
	Amount    float64 `json:"amount"`
	MarketCap int64   `json:"marketCap"`
}

type totalSponsored struct {
	PreviousTwoWeeksValue prevValue `json:"previousTwoWeeksValue"`
	PreviousWeekValue     prevValue `json:"previousWeekValue"`
	PreviousTwoDaysValue  prevValue `json:"previousTwoDaysValue"`
	PreviousDayValue      prevValue `json:"previousDayValue"`
	DiffWeek              float64   `json:"diffWeek"`
	Value                 prevValue `json:"value"`
	DiffDay               float64   `json:"diffDay"`
}

type prevValue struct {
	Weight float64 `json:"weight"`
	Power  float64 `json:"power"`
}
