package types

import (
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
)

// constants.
const (
	TypeMsgSwapExactAmountIn  = "swap_exact_amount_in"
	TypeMsgSwapExactAmountOut = "swap_exact_amount_out"
)

var ErrNotPositiveCriteria = errors.New("criteria must be positive")

var _ sdk.Msg = &MsgSwapExactAmountIn{}

func (msg MsgSwapExactAmountIn) Route() string { return RouterKey }
func (msg MsgSwapExactAmountIn) Type() string  { return TypeMsgSwapExactAmountIn }
func (msg MsgSwapExactAmountIn) ValidateBasic() error {
	_, err := sdk.AccAddressFromBech32(msg.Sender)
	if err != nil {
		return sdkerrors.Wrapf(sdkerrors.ErrInvalidAddress, "Invalid sender address (%s)", err)
	}

	err = SwapAmountInRoutes(msg.Routes).Validate()
	if err != nil {
		return err
	}

	if !msg.TokenIn.IsValid() || !msg.TokenIn.IsPositive() {
		return sdkerrors.Wrap(sdkerrors.ErrInvalidCoins, msg.TokenIn.String())
	}

	if !msg.TokenOutMinAmount.IsPositive() {
		return ErrNotPositiveCriteria
	}

	return nil
}

func (msg MsgSwapExactAmountIn) GetSignBytes() []byte {
	return sdk.MustSortJSON(ModuleCdc.MustMarshalJSON(&msg))
}

func (msg MsgSwapExactAmountIn) GetSigners() []sdk.AccAddress {
	sender, err := sdk.AccAddressFromBech32(msg.Sender)
	if err != nil {
		panic(err)
	}
	return []sdk.AccAddress{sender}
}

var _ sdk.Msg = &MsgSwapExactAmountOut{}

func (msg MsgSwapExactAmountOut) Route() string { return RouterKey }
func (msg MsgSwapExactAmountOut) Type() string  { return TypeMsgSwapExactAmountOut }
func (msg MsgSwapExactAmountOut) ValidateBasic() error {
	_, err := sdk.AccAddressFromBech32(msg.Sender)
	if err != nil {
		return sdkerrors.Wrapf(sdkerrors.ErrInvalidAddress, "Invalid sender address (%s)", err)
	}

	err = SwapAmountOutRoutes(msg.Routes).Validate()
	if err != nil {
		return err
	}

	if !msg.TokenOut.IsValid() || !msg.TokenOut.IsPositive() {
		return sdkerrors.Wrap(sdkerrors.ErrInvalidCoins, msg.TokenOut.String())
	}

	if !msg.TokenInMaxAmount.IsPositive() {
		return ErrNotPositiveCriteria
	}

	return nil
}

func (msg MsgSwapExactAmountOut) GetSignBytes() []byte {
	return sdk.MustSortJSON(ModuleCdc.MustMarshalJSON(&msg))
}

func (msg MsgSwapExactAmountOut) GetSigners() []sdk.AccAddress {
	sender, err := sdk.AccAddressFromBech32(msg.Sender)
	if err != nil {
		panic(err)
	}
	return []sdk.AccAddress{sender}
}
