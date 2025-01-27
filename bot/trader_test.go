package bot

import (
	"context"
	"errors"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestTryOpenNewposition(t *testing.T) {
	tCases := []struct {
		name           string
		targetRaise    sdk.Dec
		totalSold      sdk.Int
		spotPrice      sdk.Dec
		nav            sdk.Int
		balanceDYM     sdk.Int
		buyErr         error
		getStateErr    error
		setStateErr    error
		wantBuyPercent string
		wantErr        bool
	}{
		{
			name:           "TargetRaise == standard => 0.05, totalSold <1 => 0.05 => final = 0.05",
			targetRaise:    standardTargetRaise,
			totalSold:      sdk.NewInt(0),
			spotPrice:      sdk.MustNewDecFromStr("2.0"),
			nav:            sdk.NewInt(1000),
			balanceDYM:     sdk.NewInt(300),
			wantBuyPercent: "0.05",
		},
		{
			name:           "TargetRaise < standard => 0.1, totalSold 200 => 0.02 => final=0.1 (max(0.1,0.02))",
			targetRaise:    sdk.NewDec(9000), // < standard
			totalSold:      sdk.NewInt(200),
			spotPrice:      sdk.MustNewDecFromStr("1.0"),
			nav:            sdk.NewInt(1000),
			balanceDYM:     sdk.NewInt(300),
			wantBuyPercent: "0.1",
		},
		{
			name:           "TargetRaise > standard => 0.02, totalSold in [1..100] => 0.5 => final=0.5",
			targetRaise:    sdk.NewDec(12000), // > standard
			totalSold:      sdk.NewInt(50),
			spotPrice:      sdk.MustNewDecFromStr("1.5"),
			nav:            sdk.NewInt(1000),
			balanceDYM:     sdk.NewInt(200),
			wantBuyPercent: "0.5",
		},
		{
			name:           "buy error => expect err",
			targetRaise:    standardTargetRaise,
			totalSold:      sdk.NewInt(0),
			spotPrice:      sdk.MustNewDecFromStr("2.0"),
			nav:            sdk.NewInt(1000),
			balanceDYM:     sdk.NewInt(300),
			buyErr:         errors.New("wham"),
			wantBuyPercent: "0.05",
			wantErr:        true,
		},
		{
			name:           "getState error => expect err",
			targetRaise:    standardTargetRaise,
			totalSold:      sdk.NewInt(0),
			spotPrice:      sdk.MustNewDecFromStr("3.0"),
			nav:            sdk.NewInt(1000),
			balanceDYM:     sdk.NewInt(300),
			getStateErr:    errors.New("boom"),
			wantBuyPercent: "0.05",
			wantErr:        true,
		},
		{
			name:           "setState error => expect err",
			targetRaise:    standardTargetRaise,
			totalSold:      sdk.NewInt(0),
			spotPrice:      sdk.MustNewDecFromStr("3.0"),
			nav:            sdk.NewInt(1000),
			balanceDYM:     sdk.NewInt(300),
			setStateErr:    errors.New("bam"),
			wantBuyPercent: "0.05",
			wantErr:        true,
		},
	}

	for _, tc := range tCases {
		t.Run(tc.name, func(t *testing.T) {
			mt := &mockTrader{
				trader: trader{
					NAV:       tc.nav,
					positions: make(map[uint64]position),
					logger:    zap.NewNop(),
				},
				buyErr:      tc.buyErr,
				getStateErr: tc.getStateErr,
				setStateErr: tc.setStateErr,
			}
			mt.trader.balanceDYM = mt.getBalanceDYM
			mt.balances = sdk.NewCoins(
				sdk.NewCoin("adym", tc.balanceDYM),
			)
			mt.trader.buy = mt.buy
			mt.trader.sell = mt.sell
			mt.trader.setState = mt.setState
			mt.trader.getState = mt.getState

			mp := mockPlan{
				id:           42,
				targetRaise:  tc.targetRaise.TruncateInt(),
				totalSoldDYM: tc.totalSold,
				spotPrice:    tc.spotPrice,
			}

			ctx := context.Background()
			err := mt.tryOpenNewPosition(ctx, mp)

			if tc.wantErr {
				require.Error(t, err, "expected an error but got none")
				return
			} else {
				require.NoError(t, err, "did not expect an error but got one")
			}

			require.True(t, mt.buyCalled, "buy(...) was not called")
			assert.Equal(t, "42", mt.buyPlanID, "planID should be '42' in the buy call")

			wantDec, _ := sdk.NewDecFromStr(tc.wantBuyPercent)
			totalPortfolio := tc.nav.Add(tc.balanceDYM)
			wantAmt := sdk.NewDecFromInt(totalPortfolio).Mul(wantDec).RoundInt()

			assert.Equal(t, wantAmt.String(), mt.buySpend.String(), "the buy amt doesn't match expected allocation")

			pos, ok := mt.trader.positions[42]
			require.True(t, ok, "expected position with planID 42 to be set in trader")
			assert.Equal(t, wantAmt.Int64(), pos.valueDYM.Int64())
			spotPrice := tc.spotPrice
			expectedToken := sdk.NewDecFromInt(wantAmt).Quo(spotPrice).RoundInt()
			assert.Equal(t, expectedToken.Int64(), pos.amount.Int64())
		})
	}
}

func TestManageExistingposition(t *testing.T) {
	testCases := []struct {
		name            string
		nav             sdk.Int
		balanceDYM      sdk.Int
		plan            mockPlan
		pos             position
		oneDayVolCh     float64
		oneDayPriceCh   float64
		wantSell        bool
		wantSellPct     string
		wantBuy         bool
		wantBuyPct      string
		expectDeletePos bool
		getBalErr       error
		buyErr          error
		sellErr         error
		getStateErr     error
		setStateErr     error
		wantErr         bool
	}{
		{
			name:       "valueInDYM > 10%, partial sell path",
			nav:        sdk.NewInt(1000),
			balanceDYM: sdk.NewInt(300),
			plan: mockPlan{
				id:        42,
				spotPrice: sdk.MustNewDecFromStr("2.0"),
			},
			pos: position{
				amount: sdk.NewInt(120), // so valueInDYM = 240, i.e. 240/1000 => 24%
			},
			oneDayVolCh:   0,
			oneDayPriceCh: 0,
			wantSell:      true,
			wantSellPct:   "0.02", // sells 2% each time
		},
		{
			name:       "valueInDYM < 0.05% => entire position sold, position deleted",
			nav:        sdk.NewInt(1000000),
			balanceDYM: sdk.NewInt(500),
			plan: mockPlan{
				id:        43,
				spotPrice: sdk.MustNewDecFromStr("0.000001"), // extremely small => small value
			},
			pos:             position{amount: sdk.NewInt(1000)},
			oneDayVolCh:     0,
			oneDayPriceCh:   0,
			wantSell:        true,
			wantSellPct:     "1.00", // entire position => 100%
			expectDeletePos: true,
		},
		{
			name:       "volume drop >50% => partial 10% sell",
			nav:        sdk.NewInt(1000),
			balanceDYM: sdk.NewInt(300),
			plan: mockPlan{
				id: 44, spotPrice: sdk.MustNewDecFromStr("1.5"),
			},
			pos:           position{amount: sdk.NewInt(60)},
			oneDayVolCh:   -80,
			oneDayPriceCh: 0,
			wantSell:      true,
			wantSellPct:   "0.10",
		},
		{
			name:       "price drop 25 => trailing stop => 50% sell",
			nav:        sdk.NewInt(2000),
			balanceDYM: sdk.NewInt(400),
			plan: mockPlan{
				id: 45, spotPrice: sdk.MustNewDecFromStr("2.0"),
			},
			pos:           position{amount: sdk.NewInt(40)},
			oneDayVolCh:   0,
			oneDayPriceCh: -25,
			wantSell:      true,
			wantSellPct:   "0.50",
		},
		{
			name:       "price drop 10 => partial sell => 10%",
			nav:        sdk.NewInt(2000),
			balanceDYM: sdk.NewInt(400),
			plan: mockPlan{
				id: 46, spotPrice: sdk.MustNewDecFromStr("1.0"),
			},
			pos:           position{amount: sdk.NewInt(40)},
			oneDayVolCh:   0,
			oneDayPriceCh: -10,
			wantSell:      true,
			wantSellPct:   "0.10",
		},
		{
			name:       "price gain 200 => sells 25%",
			nav:        sdk.NewInt(2000),
			balanceDYM: sdk.NewInt(400),
			plan: mockPlan{
				id: 47, spotPrice: sdk.MustNewDecFromStr("5.0"),
			},
			pos:           position{amount: sdk.NewInt(40)},
			oneDayVolCh:   0,
			oneDayPriceCh: 201,
			wantSell:      true,
			wantSellPct:   "0.25",
		},
		{
			name:       "volume up 50 => buy 10% if dymReserveToTotal > 0.2",
			nav:        sdk.NewInt(2000),
			balanceDYM: sdk.NewInt(1000),
			plan: mockPlan{
				id: 48, spotPrice: sdk.MustNewDecFromStr("1.0"),
			},
			pos:           position{amount: sdk.NewInt(40)},
			oneDayVolCh:   60,
			oneDayPriceCh: 0,
			wantBuy:       true,
			wantBuyPct:    "0.10",
		},
		{
			name:       "price up 10 => buy 10% if dymReserve>0.2",
			nav:        sdk.NewInt(2000),
			balanceDYM: sdk.NewInt(1000),
			plan: mockPlan{
				id: 49, spotPrice: sdk.MustNewDecFromStr("1.0"),
			},
			pos:           position{amount: sdk.NewInt(40)},
			oneDayVolCh:   0,
			oneDayPriceCh: 15,
			wantBuy:       true,
			wantBuyPct:    "0.10",
		},
		{
			name:       "dymReserve ratio <= 0.2 => skip buy",
			nav:        sdk.NewInt(2000),
			balanceDYM: sdk.NewInt(400),
			plan: mockPlan{
				id: 50, spotPrice: sdk.MustNewDecFromStr("1.0"),
			},
			pos:           position{amount: sdk.NewInt(40)},
			oneDayVolCh:   60,
			oneDayPriceCh: 0,
			wantBuy:       false,
		},
		{
			name:          "getAccountBalances error => expect error",
			nav:           sdk.NewInt(10000),
			balanceDYM:    sdk.NewInt(500),
			plan:          mockPlan{id: 51, spotPrice: sdk.NewDec(1)},
			pos:           position{amount: sdk.NewInt(1)},
			oneDayVolCh:   0,
			oneDayPriceCh: 0,
			getBalErr:     errors.New("some error"),
			wantErr:       true,
		},
		{
			name:          "sell error => expect error",
			nav:           sdk.NewInt(1000),
			balanceDYM:    sdk.NewInt(500),
			plan:          mockPlan{id: 52, spotPrice: sdk.NewDec(2)},
			pos:           position{amount: sdk.NewInt(9999)},
			oneDayVolCh:   0,
			oneDayPriceCh: 0,
			sellErr:       errors.New("boom"),
			wantErr:       true,
		},
		{
			name:          "getState error => expect error",
			nav:           sdk.NewInt(1000),
			balanceDYM:    sdk.NewInt(500),
			plan:          mockPlan{id: 53, spotPrice: sdk.NewDec(2)},
			pos:           position{amount: sdk.NewInt(9999)},
			oneDayVolCh:   0,
			oneDayPriceCh: 0,
			getStateErr:   errors.New("wham"),
			wantErr:       true,
		},
		{
			name:          "setState error on sell => expect error",
			nav:           sdk.NewInt(1000),
			balanceDYM:    sdk.NewInt(500),
			plan:          mockPlan{id: 54, spotPrice: sdk.NewDec(2)},
			pos:           position{amount: sdk.NewInt(9999)},
			oneDayVolCh:   0,
			oneDayPriceCh: -25,
			setStateErr:   errors.New("bam"),
			wantErr:       true,
		},
		{
			name:          "setState error on buy => expect error",
			nav:           sdk.NewInt(1000),
			balanceDYM:    sdk.NewInt(500),
			plan:          mockPlan{id: 55, spotPrice: sdk.NewDec(2)},
			pos:           position{amount: sdk.NewInt(40)},
			oneDayVolCh:   60,
			oneDayPriceCh: 0,
			setStateErr:   errors.New("slam"),
			wantErr:       true,
		},
		{
			name:          "no buy",
			nav:           sdk.NewInt(1000),
			balanceDYM:    sdk.NewInt(500),
			plan:          mockPlan{id: 56, spotPrice: sdk.NewDec(2)},
			pos:           position{amount: sdk.NewInt(40)},
			oneDayVolCh:   0,
			oneDayPriceCh: 0,
		},
		{
			name:          "min amount error",
			nav:           sdk.NewInt(1000),
			balanceDYM:    sdk.NewInt(500),
			plan:          mockPlan{id: 57, spotPrice: sdk.NewDec(2), minAmtErr: errors.New("min amount error")},
			pos:           position{amount: sdk.NewInt(40)},
			oneDayVolCh:   0,
			oneDayPriceCh: 11,
			wantErr:       true,
		},
		{
			name:          "buy error",
			nav:           sdk.NewInt(1000),
			balanceDYM:    sdk.NewInt(500),
			plan:          mockPlan{id: 58, spotPrice: sdk.NewDec(2)},
			pos:           position{amount: sdk.NewInt(40)},
			oneDayVolCh:   60,
			oneDayPriceCh: 0,
			buyErr:        errors.New("buy error"),
			wantErr:       true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mt := &mockTrader{
				trader: trader{
					NAV: tc.nav,
					positions: map[uint64]position{
						tc.plan.id: tc.pos,
					},
					logger: zap.NewNop(),
				},
				buyErr:         tc.buyErr,
				sellErr:        tc.sellErr,
				getBalancesErr: tc.getBalErr,
				getStateErr:    tc.getStateErr,
				setStateErr:    tc.setStateErr,
			}
			// override methods
			mt.trader.getBalances = mt.getAccountBalances
			mt.trader.balanceDYM = mt.getBalanceDYM
			mt.balances = sdk.NewCoins(
				sdk.NewCoin(IRODenom(tc.plan.GetRollappId()), tc.pos.amount),
				sdk.NewCoin("adym", tc.balanceDYM),
			)
			mt.trader.buy = mt.buy
			mt.trader.sell = mt.sell
			mt.trader.setState = mt.setState
			mt.trader.getState = mt.getState

			ctx := context.Background()

			err := mt.manageExistingPosition(ctx, tc.plan, tc.pos, tc.oneDayVolCh, tc.oneDayPriceCh)

			if tc.wantErr {
				require.Error(t, err, "expected error but got none")
				return
			} else {
				require.NoError(t, err, "unexpected error")
			}

			if tc.wantSell {
				require.True(t, mt.sellCalled, "expected sell to be called, but wasn't")
				fraction, _ := sdk.NewDecFromStr(tc.wantSellPct)
				wantAmt := fraction.MulInt(tc.pos.amount).TruncateInt()
				assert.Equal(t, wantAmt.String(), mt.sellAmount.String(), "sell amount mismatch")
			} else {
				require.False(t, mt.sellCalled, "expected no sell, but got one")
			}

			if tc.wantBuy {
				require.True(t, mt.buyCalled, "expected buy to be called, but wasn't")
				fraction, _ := sdk.NewDecFromStr(tc.wantBuyPct)
				wantAmt := fraction.MulInt(tc.pos.amount).TruncateInt()
				assert.Equal(t, wantAmt.String(), mt.buySpend.String(), "buy amount mismatch")
			} else {
				require.False(t, mt.buyCalled, "expected no buy, but got one")
			}

			if tc.expectDeletePos {
				_, found := mt.trader.positions[tc.plan.id]
				require.False(t, found, "expected position to be deleted, but still found")
			}
		})
	}
}

type mockTrader struct {
	trader

	buyCalled, sellCalled bool
	buySpend, sellAmount  sdk.Int
	buyMin, sellMin       sdk.Int
	buyPlanID, sellPlanID string
	newpositionsMap       map[uint64]position
	deletedpositions      []uint64
	balances              sdk.Coins

	buyErr, sellErr, getBalancesErr, getStateErr, setStateErr error
}

func (mt *mockTrader) buy(_ context.Context, spend, minAmount sdk.Int, planID string) error {
	mt.buyCalled = true
	mt.buySpend = spend
	mt.buyMin = minAmount
	mt.buyPlanID = planID
	return mt.buyErr
}

func (mt *mockTrader) sell(_ context.Context, amount, minIncome sdk.Int, planID string) error {
	mt.sellCalled = true
	mt.sellAmount = amount
	mt.sellMin = minIncome
	mt.sellPlanID = planID
	return mt.sellErr
}

func (mt *mockTrader) getAccountBalances(_ context.Context) (sdk.Coins, error) {
	return mt.balances, mt.getBalancesErr
}

func (mt *mockTrader) getBalanceDYM() sdk.Int {
	return mt.balances.AmountOf("adym")
}

func (mt *mockTrader) setState(*state) error {
	return mt.setStateErr
}

func (mt *mockTrader) getState() (*state, error) {
	st := &state{
		Positions: map[string]positionState{},
	}
	return st, mt.getStateErr
}

type mockPlan struct {
	id           uint64
	targetRaise  sdk.Int
	totalSoldDYM sdk.Int
	spotPrice    sdk.Dec
	minAmtErr    error
}

func (m mockPlan) GetId() uint64                        { return m.id }
func (m mockPlan) GetRollappId() string                 { return "mock_123-1" }
func (m mockPlan) TargetRaise() sdk.Int                 { return m.targetRaise }
func (m mockPlan) TotalSoldInDYM() sdk.Int              { return m.totalSoldDYM }
func (m mockPlan) SpotPrice() sdk.Dec                   { return m.spotPrice }
func (m mockPlan) MinIncome(s sdk.Int) sdk.Int          { return s }
func (m mockPlan) MinAmount(s sdk.Int) (sdk.Int, error) { return s, m.minAmtErr }
