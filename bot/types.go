package bot

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/dymensionxyz/eco-bot/types"
)

type account struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

func tokensForDYM(plan *types.Plan, amt sdk.Int) (sdk.Coin, error) {
	tokensAmt, err := plan.BondingCurve.TokensForExactDYM(plan.SoldAmt, amt)
	if err != nil {
		return sdk.Coin{}, err
	}

	return sdk.NewCoin(IRODenom(plan.RollappId), tokensAmt), nil
}

const IROTokenPrefix = "IRO/"

func IRODenom(rollappID string) string {
	return fmt.Sprintf("%s%s", IROTokenPrefix, rollappID)
}

type analyticsResp struct {
	Volume         volume         `json:"volume"`
	CreationDate   int64          `json:"creationDate"`
	Currencies     []currency     `json:"currencies"`
	Bech32Prefix   string         `json:"bech32Prefix"`
	Launched       bool           `json:"launched"`
	Type           string         `json:"type"`
	CoinType       int            `json:"coinType"`
	PreLaunchTime  int64          `json:"preLaunchTime"`
	InitialSupply  float64        `json:"initialSupply"`
	Status         string         `json:"status"`
	TotalSupply    totalSupply    `json:"totalSupply"`
	TotalSponsored totalSponsored `json:"totalSponsored"`
}

type volume struct {
	DiffWeek              int `json:"diffWeek"`
	PreviousWeekValue     int `json:"previousWeekValue"`
	PreviousTwoDaysValue  int `json:"previousTwoDaysValue"`
	PreviousTwoWeeksValue int `json:"previousTwoWeeksValue"`
	PreviousDayValue      int `json:"previousDayValue"`
	Value                 int `json:"value"`
	DiffDay               int `json:"diffDay"`
}

type currency struct {
	DisplayDenom string `json:"displayDenom"`
	BaseDenom    string `json:"baseDenom"`
	Decimals     int    `json:"decimals"`
	Type         string `json:"type"`
}

type totalSupply struct {
	PreviousTwoWeeksValue int   `json:"previousTwoWeeksValue"`
	DiffDay               int   `json:"diffDay"`
	DiffWeek              int   `json:"diffWeek"`
	Value                 value `json:"value"`
	PreviousTwoDaysValue  value `json:"previousTwoDaysValue"`
	PreviousDayValue      value `json:"previousDayValue"`
	PreviousWeekValue     value `json:"previousWeekValue"`
}

type value struct {
	Amount    float64 `json:"amount"`
	MarketCap int64   `json:"marketCap"`
}

type totalSponsored struct {
	PreviousTwoWeeksValue int       `json:"previousTwoWeeksValue"`
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
