package types

import (
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/auth/migrations/legacytx"
)

const (
	TypeMsgBuy        = "buy"
	TypeMsgExactSpend = "buy_exact_spend"
	TypeMsgSell       = "sell"
	TypeMsgClaim      = "claim"

	ModuleName = "iro"
	RouterKey  = ModuleName
)

var (
	_ sdk.Msg            = &MsgBuy{}
	_ sdk.Msg            = &MsgBuyExactSpend{}
	_ sdk.Msg            = &MsgSell{}
	_ sdk.Msg            = &MsgClaim{}
	_ legacytx.LegacyMsg = &MsgBuy{}
	_ legacytx.LegacyMsg = &MsgBuyExactSpend{}
	_ legacytx.LegacyMsg = &MsgSell{}
	_ legacytx.LegacyMsg = &MsgClaim{}
)

// MsgBuy

func (m *MsgBuy) Route() string {
	return RouterKey
}

func (m *MsgBuy) Type() string {
	return TypeMsgBuy
}

func (m *MsgBuy) GetSigners() []sdk.AccAddress {
	addr := sdk.MustAccAddressFromBech32(m.Buyer)
	return []sdk.AccAddress{addr}
}

func (m *MsgBuy) GetSignBytes() []byte {
	bz := ModuleCdc.MustMarshalJSON(m)
	return sdk.MustSortJSON(bz)
}

func (m *MsgBuy) ValidateBasic() error {
	// buyer bech32
	_, err := sdk.AccAddressFromBech32(m.Buyer)
	if err != nil {
		return sdkerrors.ErrInvalidAddress.Wrapf("invalid buyer address: %s", err)
	}

	// coin exist and valid
	if m.Amount.IsNil() || !m.Amount.IsPositive() {
		return sdkerrors.ErrInvalidRequest.Wrapf("amount %v must be positive", m.Amount)
	}

	if m.MaxCostAmount.IsNil() || !m.MaxCostAmount.IsPositive() {
		return sdkerrors.ErrInvalidRequest.Wrapf("expected out amount %v must be positive", m.MaxCostAmount)
	}

	return nil
}

func (*MsgBuy) XXX_MessageName() string {
	return "dymensionxyz.dymension.iro.MsgBuy"
}

// MsgBuyExactSpend

func (m *MsgBuyExactSpend) Route() string {
	return RouterKey
}

func (m *MsgBuyExactSpend) Type() string {
	return TypeMsgExactSpend
}

func (m *MsgBuyExactSpend) GetSigners() []sdk.AccAddress {
	addr := sdk.MustAccAddressFromBech32(m.Buyer)
	return []sdk.AccAddress{addr}
}

func (m *MsgBuyExactSpend) GetSignBytes() []byte {
	bz := ModuleCdc.MustMarshalJSON(m)
	return sdk.MustSortJSON(bz)
}

func (m *MsgBuyExactSpend) ValidateBasic() error {
	// buyer bech32
	_, err := sdk.AccAddressFromBech32(m.Buyer)
	if err != nil {
		return sdkerrors.ErrInvalidAddress.Wrapf("invalid buyer address: %s", err)
	}

	// coin exist and valid
	if m.Spend.IsNil() || !m.Spend.IsPositive() {
		return sdkerrors.ErrInvalidRequest.Wrapf("amount %v must be positive", m.Spend)
	}

	if m.MinOutTokensAmount.IsNil() || !m.MinOutTokensAmount.IsPositive() {
		return sdkerrors.ErrInvalidRequest.Wrapf("expected out amount %v must be positive", m.MinOutTokensAmount)
	}

	return nil
}

func (*MsgBuyExactSpend) XXX_MessageName() string {
	return "dymensionxyz.dymension.iro.MsgBuyExactSpend"
}

// MsgSell

func (m *MsgSell) Route() string {
	return RouterKey
}

func (m *MsgSell) Type() string {
	return TypeMsgSell
}

func (m *MsgSell) GetSigners() []sdk.AccAddress {
	addr := sdk.MustAccAddressFromBech32(m.Seller)
	return []sdk.AccAddress{addr}
}

func (m *MsgSell) GetSignBytes() []byte {
	bz := ModuleCdc.MustMarshalJSON(m)
	return sdk.MustSortJSON(bz)
}

func (m *MsgSell) ValidateBasic() error {
	// seller bech32
	_, err := sdk.AccAddressFromBech32(m.Seller)
	if err != nil {
		return sdkerrors.ErrInvalidAddress.Wrapf("invalid seller address: %s", err)
	}

	// coin exist and valid
	if m.Amount.IsNil() || !m.Amount.IsPositive() {
		return sdkerrors.ErrInvalidRequest.Wrapf("amount %v must be positive", m.Amount)
	}

	if m.MinIncomeAmount.IsNil() || !m.MinIncomeAmount.IsPositive() {
		return sdkerrors.ErrInvalidRequest.Wrapf("expected out amount %v must be positive", m.MinIncomeAmount)
	}

	return nil
}

func (*MsgSell) XXX_MessageName() string {
	return "dymensionxyz.dymension.iro.MsgSell"
}

// MsgClaim

func (m *MsgClaim) Route() string {
	return RouterKey
}

func (m *MsgClaim) Type() string {
	return TypeMsgClaim
}

func (m *MsgClaim) GetSigners() []sdk.AccAddress {
	addr := sdk.MustAccAddressFromBech32(m.Claimer)
	return []sdk.AccAddress{addr}
}

func (m *MsgClaim) GetSignBytes() []byte {
	bz := ModuleCdc.MustMarshalJSON(m)
	return sdk.MustSortJSON(bz)
}

func (m *MsgClaim) ValidateBasic() error {
	// claimer bech32
	_, err := sdk.AccAddressFromBech32(m.Claimer)
	if err != nil {
		return sdkerrors.ErrInvalidAddress.Wrapf("invalid claimer address: %s", err)
	}

	return nil
}

func (*MsgClaim) XXX_MessageName() string {
	return "dymensionxyz.dymension.iro.MsgClaim"
}
