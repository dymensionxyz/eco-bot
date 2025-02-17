package bot

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	channeltypes "github.com/cosmos/ibc-go/v6/modules/core/04-channel/types"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
)

type (
	iteratePlanCallback    func(plan iroPlan) (bool, error)
	iterateRollappCallback func(rollapp) (bool, error)
)

type position struct {
	valueDYM  sdk.Int
	amount    sdk.Int
	createdAt time.Time
	isIRO     bool
}

type account struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type state struct {
	Positions map[string]positionState `json:"positions"`
	LastTrade int64                    `json:"lastTrade"`
	NextTrade int64                    `json:"nextTrade"`
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
	IBCDenom     string `json:"rollappIbcRepresentation"`
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

type rollappsResp []rollapp

type rollapp struct {
	ChainID    string     `json:"chainId"`
	Status     string     `json:"status"`
	Liquidity  sdk.Int    `json:"liquidity"`
	IBC        ibc        `json:"ibc"`
	Currencies []currency `json:"currencies"`
	IBCDenom   string
	PoolID     uint64
}

type gammPoolsResp struct {
	Pools []gammPool `json:"pools"`
}
type gammPool struct {
	Type       string `json:"@type"`
	Address    string `json:"address"`
	Id         string `json:"id"`
	PoolParams struct {
		SwapFee                  string      `json:"swap_fee"`
		ExitFee                  string      `json:"exit_fee"`
		SmoothWeightChangeParams interface{} `json:"smooth_weight_change_params"`
	} `json:"pool_params"`
	FuturePoolGovernor string `json:"future_pool_governor"`
	TotalShares        struct {
		Denom  string `json:"denom"`
		Amount string `json:"amount"`
	} `json:"total_shares"`
	PoolAssets []struct {
		Token struct {
			Denom  string `json:"denom"`
			Amount string `json:"amount"`
		} `json:"token"`
		Weight string `json:"weight"`
	} `json:"pool_assets"`
	TotalWeight string `json:"total_weight"`
}

type ibc struct {
	Timeout    int    `json:"timeout"`
	HubChannel string `json:"hubChannel"`
	Channel    string `json:"channel"`
}

type DenomTrace struct {
	// path defines the chain of port/channel identifiers used for tracing the
	// source of the fungible token.
	Path string `protobuf:"bytes,1,opt,name=path,proto3" json:"path,omitempty"`
	// base denomination of the relayed fungible token.
	BaseDenom string `protobuf:"bytes,2,opt,name=base_denom,json=baseDenom,proto3" json:"base_denom,omitempty"`
}

func ParseDenomTrace(rawDenom string) DenomTrace {
	denomSplit := strings.Split(rawDenom, "/")

	if denomSplit[0] == rawDenom {
		return DenomTrace{
			Path:      "",
			BaseDenom: rawDenom,
		}
	}

	path, baseDenom := extractPathAndBaseFromFullDenom(denomSplit)
	return DenomTrace{
		Path:      path,
		BaseDenom: baseDenom,
	}
}

func (dt DenomTrace) Hash() tmbytes.HexBytes {
	hash := sha256.Sum256([]byte(dt.GetFullDenomPath()))
	return hash[:]
}

func extractPathAndBaseFromFullDenom(fullDenomItems []string) (string, string) {
	var (
		path      []string
		baseDenom []string
	)

	length := len(fullDenomItems)
	for i := 0; i < length; i += 2 {
		if i < length-1 && length > 2 && channeltypes.IsValidChannelID(fullDenomItems[i+1]) {
			path = append(path, fullDenomItems[i], fullDenomItems[i+1])
		} else {
			baseDenom = fullDenomItems[i:]
			break
		}
	}

	return strings.Join(path, "/"), strings.Join(baseDenom, "/")
}

func (dt DenomTrace) GetFullDenomPath() string {
	if dt.Path == "" {
		return dt.BaseDenom
	}
	return dt.GetPrefix() + dt.BaseDenom
}

func (dt DenomTrace) GetPrefix() string {
	return dt.Path + "/"
}
