package bot

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/dymensionxyz/cosmosclient/cosmosclient"
	"go.uber.org/zap"

	"github.com/dymensionxyz/eco-bot/config"
)

type bot struct {
	traders []*trader
	pm      *positionManager
	whale   *whale

	logger *zap.Logger
}

func NewBot(cfg config.Config, logger *zap.Logger) (*bot, error) {
	sdkcfg := sdk.GetConfig()
	sdkcfg.SetBech32PrefixForAccount(config.HubAddressPrefix, config.PubKeyPrefix)

	traderClientCfg := config.ClientConfig{
		HomeDir:        cfg.Traders.KeyringDir,
		KeyringBackend: cfg.Traders.KeyringBackend,
		NodeAddress:    cfg.NodeAddress,
		GasFees:        cfg.Gas.Fees,
		GasPrices:      cfg.Gas.Prices,
	}

	pmClient, err := cosmosclient.New(config.GetCosmosClientOptions(traderClientCfg)...)
	if err != nil {
		return nil, fmt.Errorf("failed to create cosmos client for position manager: %w", err)
	}

	minGasBalance, err := sdk.ParseCoinNormalized(cfg.Gas.MinimumGasBalance)
	if err != nil {
		return nil, fmt.Errorf("failed to parse minimum gas balance: %w", err)
	}

	subscriberId := fmt.Sprintf("subscriber-%s", cfg.Whale.AccountName)
	q := newQuerier(pmClient, cfg.AnalyticsURL)
	e := newEventer(pmClient.RPC, pmClient.WSEvents, subscriberId, logger)
	pm := newPositionManager(q, e, cfg.Traders.PositionManageInterval, logger)
	topUpCh := make(chan topUpRequest, cfg.Traders.Scale)

	whaleSvc, err := buildWhale(
		logger,
		cfg.Whale,
		cfg.NodeAddress,
		cfg.Gas.Fees,
		cfg.Gas.Prices,
		minGasBalance,
		topUpCh,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create whale: %w", err)
	}

	accs, err := addTraderAccounts(cfg.Traders.Scale, traderClientCfg, logger)
	if err != nil {
		return nil, err
	}

	activeAccs := make([]account, 0, len(accs))
	traders := make(map[string]*trader)

	var traderIdx int
	for traderIdx = range cfg.Traders.Scale {
		acc := accs[traderIdx]

		cClient, err := cosmosclient.New(config.GetCosmosClientOptions(traderClientCfg)...)
		if err != nil {
			return nil, fmt.Errorf("failed to create cosmos client for trader: %s;err: %w", acc.Name, err)
		}

		as, err := newAccountService(
			cClient,
			logger,
			acc.Name,
			minGasBalance,
			topUpCh,
			// withTopUpFactor(cfg.TopUpFactor),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create account service for bot: %s;err: %w", acc.Name, err)
		}

		getState, setState := newStateKeeper(fmt.Sprintf("%s/state-%s.json", cfg.Traders.KeyringDir, acc.Name))

		t := newTrader(
			as,
			q,
			pm.iteratePlans,
			cClient,
			setState,
			getState,
			cfg.Traders.PositionManageInterval,
			logger,
		)

		traders[t.accountSvc.accountName] = t
		activeAccs = append(activeAccs, acc)
	}

	traderList := make([]*trader, 0, len(traders))
	for _, t := range traders {
		traderList = append(traderList, t)
	}

	return &bot{
		pm:      pm,
		whale:   whaleSvc,
		logger:  logger,
		traders: traderList,
	}, nil
}

func (b bot) Start(ctx context.Context) {
	if err := b.pm.start(ctx); err != nil {
		b.logger.Error("failed to start position manager", zap.Error(err))
		return
	}

	if err := b.whale.start(ctx); err != nil {
		b.logger.Error("failed to start whale", zap.Error(err))
		return
	}

	for _, t := range b.traders {
		go t.managePositions()
	}

	<-ctx.Done()
}
