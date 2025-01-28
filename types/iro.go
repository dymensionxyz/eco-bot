package types

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/shopspring/decimal"
)

/*

export const fetchPlanTargetRaise = (plan: Plan, part = 100): number => {
    if (!plan.bondingCurve || !plan.totalAllocation) {
        return 0;
    }
    const curve = convertToBondingCurve(plan.bondingCurve);
    const allocation = Decimal.fromAtomics(plan.totalAllocation.amount, 18).toFloatApproximation() * part / 100;
    return curve.M === 0 ? curve.C * allocation : curve.M * (allocation ** (curve.N + 1)) / (curve.N + 1);
};

*/

func (p Plan) TargetRaise() sdk.Dec {
	ta := sdk.NewDecFromInt(p.TotalAllocation.Amount)
	if ta.IsZero() {
		ta = sdk.NewDec(1)
	}
	iroProgress := sdk.NewDecFromInt(p.SoldAmt).Mul(sdk.NewDec(100)).Quo(ta)
	allocation := sdk.NewDecFromInt(p.TotalAllocation.Amount).Quo(sdk.NewDec(10).Power(18)).Mul(iroProgress).Quo(sdk.NewDec(100))
	targetRaise := sdk.NewDec(0)
	if p.BondingCurve.M.IsZero() {
		targetRaise = p.BondingCurve.C.Mul(allocation)
	} else {
		nPlusOne := p.BondingCurve.N.Add(sdk.NewDec(1))
		dDec := decimal.NewFromBigInt(allocation.BigInt(), -sdk.Precision)
		power, err := dDec.PowWithPrecision(decimal.RequireFromString(nPlusOne.String()), -sdk.Precision)
		if err != nil {
			panic(err)
		}
		targetRaise = p.BondingCurve.M.Mul(sdk.MustNewDecFromStr(power.StringFixed(-4))).Quo(nPlusOne)
	}
	return targetRaise
}

func (p Plan) MinIncome(amt sdk.Int) sdk.Int {
	minIncome := p.GetBondingCurve().Cost(p.GetSoldAmt().Sub(amt), p.GetSoldAmt())
	trimAmt := minIncome.Quo(sdk.NewInt(10)) // remove 10% for taker fee and whatnot
	// return minIncome.Sub(trimAmt)
	return trimAmt
}

func (p Plan) MinAmount(spend sdk.Int) (sdk.Int, error) {
	// get the min amount
	minAmount, err := p.GetBondingCurve().TokensForExactDYM(p.GetSoldAmt(), spend)
	if err != nil {
		return sdk.Int{}, fmt.Errorf("failed to get tokens for exact DYM: %w", err)
	}
	trimAmt := minAmount.Quo(sdk.NewInt(10)) // remove 10% for taker fee and whatnot
	// return minAmount.Sub(trimAmt), nil
	return trimAmt, nil
}

func (p Plan) GetSoldAmt() sdk.Int {
	return p.SoldAmt
}
