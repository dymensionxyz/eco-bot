package bot

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/dymensionxyz/cosmosclient/cosmosclient"
	"go.uber.org/zap"

	"github.com/dymensionxyz/eco-bot/config"
)

type whale struct {
	accountSvc        *accountService
	logger            *zap.Logger
	topUpCh           <-chan topUpRequest
	balanceThresholds map[string]sdk.Coin
	chainID, node     string
}

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
		accountSvc:        accountSvc,
		logger:            logger.With(zap.String("module", "whale")),
		topUpCh:           topUpCh,
		balanceThresholds: balanceThresholds,
		chainID:           chainID,
		node:              node,
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
			toppedUp := w.topUp(ctx, req.coins, req.toAddr)
			req.res <- toppedUp
		}
	}
}

func (w *whale) topUp(ctx context.Context, coins sdk.Coins, toAddr string) []string {
	whaleBalances, err := w.accountSvc.getAccountBalances(ctx)
	if err != nil {
		w.logger.Error("failed to get account balances", zap.Error(err))
		return nil
	}

	canTopUp := sdk.NewCoins()
	for _, coin := range coins {
		balance := whaleBalances.AmountOf(coin.Denom)

		diff := balance.Sub(coin.Amount)
		if diff.IsPositive() {
			canTopUp = canTopUp.Add(coin)
		}
	}

	if canTopUp.Empty() {
		w.logger.Debug(
			"no denoms to top up",
			zap.String("to", toAddr),
			zap.String("coins", coins.String()))

		return nil
	}

	w.logger.Debug(
		"topping up account",
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
