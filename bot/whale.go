package bot

import (
	"context"
	"fmt"
	"sync"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/dymensionxyz/cosmosclient/cosmosclient"
	"go.uber.org/zap"

	"github.com/dymensionxyz/eco-bot/config"
)

type whale struct {
	accountSvc           *accountService
	logger               *zap.Logger
	topUpCh              <-chan topUpRequest
	balanceThresholds    map[string]sdk.Coin
	chainID, node        string
	iamu                 sync.Mutex
	intermediaryAccounts map[string]*accountService
}

const intermediaryTopUpFactor = 4

type topUpRequest struct {
	coins  sdk.Coins
	toAddr string
	res    chan []string
}

func newWhale(
	accountSvc *accountService,
	balanceThresholds map[string]sdk.Coin,
	logger *zap.Logger,
	chainID, node string,
	topUpCh <-chan topUpRequest,
) *whale {
	return &whale{
		accountSvc:           accountSvc,
		intermediaryAccounts: make(map[string]*accountService),
		logger:               logger.With(zap.String("module", "whale")),
		topUpCh:              topUpCh,
		balanceThresholds:    balanceThresholds,
		chainID:              chainID,
		node:                 node,
	}
}

func buildWhale(
	logger *zap.Logger,
	wcfg config.WhaleConfig,
	nodeAddress, gasFees, gasPrices string,
	minimumGasBalance sdk.Coin,
	topUpCh chan topUpRequest,
) (*whale, error) {
	clientCfg := config.ClientConfig{
		HomeDir:        wcfg.KeyringDir,
		KeyringBackend: wcfg.KeyringBackend,
		NodeAddress:    nodeAddress,
		GasFees:        gasFees,
		GasPrices:      gasPrices,
	}

	balanceThresholdMap := make(map[string]sdk.Coin)
	for denom, threshold := range wcfg.AllowedBalanceThresholds {
		coinStr := threshold + denom
		coin, err := sdk.ParseCoinNormalized(coinStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse threshold coin: %w", err)
		}

		balanceThresholdMap[denom] = coin
	}

	cosmosClient, err := cosmosclient.New(config.GetCosmosClientOptions(clientCfg)...)
	if err != nil {
		return nil, fmt.Errorf("failed to create cosmos client for whale: %w", err)
	}

	accountSvc, err := newAccountService(
		cosmosClient,
		logger,
		wcfg.AccountName,
		minimumGasBalance,
		topUpCh,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create account service for whale: %w", err)
	}

	return newWhale(
		accountSvc,
		balanceThresholdMap,
		logger,
		cosmosClient.Context().ChainID,
		clientCfg.NodeAddress,
		topUpCh,
	), nil
}

func (w *whale) start(ctx context.Context) error {
	balances, err := w.accountSvc.getAccountBalances(ctx)
	if err != nil {
		return fmt.Errorf("failed to get account balances: %w", err)
	}

	w.logger.Info("starting service...",
		zap.String("account", w.accountSvc.accountName),
		zap.String("address", w.accountSvc.address()),
		zap.String("balances", balances.String()))

	go w.topUpBalances(ctx)
	return nil
}

func (w *whale) topUpBalances(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-w.topUpCh:
			// all accounts will be topped up from an intermediary account
			toppedUp := w.topUpFromIntermediary(ctx, req.coins, req.toAddr)
			req.res <- toppedUp
		}
	}
}

func (w *whale) topUpFromWhale(ctx context.Context, coins sdk.Coins, toAddr string) []string {
	// intermediary accounts should be topped up a higher amount, such as 4x
	w.iamu.Lock()
	_, ok := w.intermediaryAccounts[toAddr]
	w.iamu.Unlock()

	if ok {
		coinsTemp := sdk.NewCoins()
		for _, coin := range coins {
			coinsTemp = coinsTemp.Add(sdk.NewCoin(coin.Denom, coin.Amount.MulRaw(intermediaryTopUpFactor)))
		}
		coins = coinsTemp
	}

	whaleBalances, err := w.accountSvc.getAccountBalances(ctx)
	if err != nil {
		w.logger.Error("failed to get account balances", zap.Error(err))
		return nil
	}

	canTopUp := sdk.NewCoins()
	for _, coin := range coins {
		balance := whaleBalances.AmountOf(coin.Denom)

		diff := balance.Sub(coin.Amount)
		if !diff.IsPositive() {
			w.logger.Warn(
				"whale balance is below threshold; waiting for top-up",
				zap.String("denom", coin.Denom),
				zap.String("balance", balance.String()),
				zap.String("threshold", coin.Amount.String()),
			)

			if err = w.waitForWhaleTopUp(ctx, coin); err != nil {
				w.logger.Error("failed to wait for whale top-up", zap.Error(err))
				return nil
			}
		}
		canTopUp = canTopUp.Add(coin)
	}

	if canTopUp.Empty() {
		w.logger.Debug(
			"no denoms to top up",
			zap.String("to", toAddr),
			zap.String("coins", coins.String()))

		return nil
	}

	w.logger.Debug(
		"topping up from whale",
		zap.String("to", toAddr),
		zap.String("coins", canTopUp.String()),
	)

	if err = w.accountSvc.sendCoins(ctx, canTopUp, toAddr); err != nil {
		w.logger.Error("failed to top up account", zap.Error(err))
		return nil
	}

	toppedUp := make([]string, len(canTopUp))
	for i, coin := range canTopUp {
		toppedUp[i] = coin.Denom
	}

	return toppedUp
}

// topUpFromIntermediary tops up the account from one of the intermediary accounts
// if the intermediary account doesn't have enough balance to send the coins, it will top up from the whale first
func (w *whale) topUpFromIntermediary(ctx context.Context, coins sdk.Coins, toAddr string) []string {
	var intAcc *accountService
	// pick a random intermediary account
	w.iamu.Lock()
	for _, ia := range w.intermediaryAccounts {
		if ia.address() == toAddr {
			continue
		}
		intAcc = ia
		break
	}
	w.iamu.Unlock()

	if intAcc == nil {
		w.logger.Debug("no intermediary account available - topping up from whale account")
		return w.topUpFromWhale(ctx, coins, toAddr)
	}

	w.logger.Debug(
		"topping up from intermediary",
		zap.String("from", intAcc.accountName),
		zap.String("to", toAddr),
		zap.String("coins", coins.String()),
	)

	// get the balances of the intermediary account
	intermediaryBalances, err := intAcc.getAccountBalances(ctx)
	if err != nil {
		w.logger.Error("failed to get intermediary account balances", zap.Error(err))
		return nil
	}

	toTopUpIntermediary := sdk.NewCoins()

	for _, coin := range coins {
		iBalance := intermediaryBalances.AmountOf(coin.Denom)
		// if the intermediary account hasn't enough balance, fund intermediary account from whale
		if iBalance.LT(coin.Amount) {
			toTopUpIntermediary = toTopUpIntermediary.Add(coin)
		}
	}

	// if there are coins to top up intermediary account, top up from whale
	if !toTopUpIntermediary.Empty() {
		toppedUpInterDenoms := w.topUpFromWhale(ctx, toTopUpIntermediary, intAcc.address())
		if toppedUpInterDenoms == nil {
			w.logger.Error("failed to top up intermediary account")
			return nil
		}
	}

	// send the coins from intermediary account to the target account
	if err = intAcc.sendCoins(ctx, coins, toAddr); err != nil {
		w.logger.Error("failed to send coins from intermediary account", zap.Error(err))
		return nil
	}

	toppedUp := make([]string, len(coins))
	for i, coin := range coins {
		toppedUp[i] = coin.Denom
	}

	return toppedUp
}

func (w *whale) addIntermediaryAccount(acc *accountService) {
	w.iamu.Lock()
	w.intermediaryAccounts[acc.address()] = acc
	w.logger.Info(
		"added intermediary account",
		zap.String("name", acc.accountName),
		zap.String("address", acc.address()))
	w.iamu.Unlock()
}

func (w *whale) waitForWhaleTopUp(ctx context.Context, threshold sdk.Coin) error {
	t := time.NewTicker(1 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context done")
		case <-t.C:
			balances, err := w.accountSvc.getAccountBalances(ctx)
			if err != nil {
				return fmt.Errorf("failed to get account balances: %w", err)
			}

			if balances.AmountOf(threshold.Denom).GT(threshold.Amount) {
				w.logger.Info(
					"balance topped up",
					zap.String("denom", threshold.Denom),
					zap.String("balance", balances.AmountOf(threshold.Denom).String()),
					zap.String("threshold", threshold.Amount.String()),
				)
				return nil
			}
		}
	}
}
