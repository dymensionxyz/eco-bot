package types

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

func (p Plan) TargetRaise() sdk.Int {
	allocation := p.TotalAllocation.Amount.Quo(sdk.NewDec(10).Power(18).TruncateInt())
	targetRaise := sdk.NewDec(0)
	if p.BondingCurve.M.IsZero() {
		targetRaise = p.BondingCurve.C.Mul(sdk.NewDecFromInt(allocation))
	} else {
		targetRaise = p.BondingCurve.M.
			Mul(sdk.NewDecFromInt(allocation).Power(uint64(p.BondingCurve.N.Add(sdk.NewDec(1)).TruncateInt64()))).
			Quo(p.BondingCurve.N.Add(sdk.NewDec(1)))
	}
	return targetRaise.TruncateInt()
}

func (p Plan) MinIncome(amt sdk.Int) sdk.Int {
	minIncome := p.GetBondingCurve().Cost(p.GetSoldAmt().Sub(amt), p.GetSoldAmt())
	trimAmt := minIncome.Quo(sdk.NewInt(10)) // remove 10% for taker fee and whatnot
	return minIncome.Sub(trimAmt)
}

func (p Plan) MinAmount(spend sdk.Int) (sdk.Int, error) {
	// get the min amount
	minAmount, err := p.GetBondingCurve().TokensForExactDYM(p.GetSoldAmt(), spend)
	if err != nil {
		return sdk.Int{}, fmt.Errorf("failed to get tokens for exact DYM: %w", err)
	}
	trimAmt := minAmount.Quo(sdk.NewInt(10)) // remove 10% for taker fee and whatnot
	return minAmount.Sub(trimAmt), nil
}

func (p Plan) GetSoldAmt() sdk.Int {
	return p.SoldAmt
}
