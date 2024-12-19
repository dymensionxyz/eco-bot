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
	cfg config.Config

	traders []*trader
	pm      *positionManager

	logger *zap.Logger
}

func NewBot(cfg config.Config, logger *zap.Logger) (*bot, error) {
	sdkcfg := sdk.GetConfig()
	sdkcfg.SetBech32PrefixForAccount(config.HubAddressPrefix, config.PubKeyPrefix)

	operatorClientCfg := config.ClientConfig{
		HomeDir:        cfg.Operator.KeyringDir,
		KeyringBackend: cfg.Operator.KeyringBackend,
		NodeAddress:    cfg.NodeAddress,
		GasFees:        cfg.Gas.Fees,
		GasPrices:      cfg.Gas.Prices,
	}

	operatorClient, err := cosmosclient.New(config.GetCosmosClientOptions(operatorClientCfg)...)
	if err != nil {
		return nil, fmt.Errorf("failed to create cosmos client for trader: %w", err)
	}

	operatorName := cfg.Operator.AccountName
	operatorAddress, err := operatorClient.Address(operatorName)
	if err != nil {
		return nil, fmt.Errorf("failed to get operator address: %w", err)
	}

	traderClientCfg := config.ClientConfig{
		HomeDir:        cfg.Traders.KeyringDir,
		KeyringBackend: cfg.Traders.KeyringBackend,
		NodeAddress:    cfg.NodeAddress,
		GasFees:        cfg.Gas.Fees,
		GasPrices:      cfg.Gas.Prices,
		FeeGranter:     operatorAddress.String(),
	}

	pmClient, err := cosmosclient.New(config.GetCosmosClientOptions(traderClientCfg)...)
	if err != nil {
		return nil, fmt.Errorf("failed to create cosmos client for position manager: %w", err)
	}

	subscriberId := fmt.Sprintf("subscriber-%s", cfg.Traders.PolicyAddress)
	q := newQuerier(pmClient)
	e := newEventer(pmClient.RPC, pmClient.WSEvents, subscriberId, logger)

	pm := newPositionManager(q, e, logger)

	accs, err := addTraderAccounts(cfg.Traders.Scale, traderClientCfg, logger)
	if err != nil {
		return nil, err
	}

	activeAccs := make([]account, 0, len(accs))
	granteeAddrs := make([]sdk.AccAddress, 0, len(accs))
	primeAddrs := make([]string, 0, len(accs))
	traders := make(map[string]*trader)

	var traderIdx int
	for traderIdx = range cfg.Traders.Scale {
		acc := accs[traderIdx]
		exist, err := accountExists(operatorClient.Context(), acc.Address)
		if err != nil {
			return nil, fmt.Errorf("failed to check if trader account exists: %w", err)
		}
		if !exist {
			primeAddrs = append(primeAddrs, acc.Address)
		}

		/*hasGrant, err := hasFeeGranted(operatorClient, operatorAddress.String(), acc.Address)
		if err != nil {
			return nil, fmt.Errorf("failed to check if fee is granted: %w", err)
		}
		if !hasGrant {
			_, granteeAddr, err := bech32.DecodeAndConvert(acc.Address)
			if err != nil {
				return nil, fmt.Errorf("failed to decode trader address: %w", err)
			}
			granteeAddrs = append(granteeAddrs, granteeAddr)
		}*/

		cClient, err := cosmosclient.New(config.GetCosmosClientOptions(traderClientCfg)...)
		if err != nil {
			return nil, fmt.Errorf("failed to create cosmos client for trader: %s;err: %w", acc.Name, err)
		}

		t := newTrader(
			acc,
			q,
			pm.iteratePlans,
			cfg.Traders.PolicyAddress,
			operatorAddress.String(),
			cClient,
			logger,
		)

		traders[t.account.Name] = t
		activeAccs = append(activeAccs, acc)
	}

	if len(primeAddrs) > 0 {
		logger.Info("priming trader accounts", zap.Strings("addresses", primeAddrs))
		if err = primeAccounts(operatorClient, operatorName, operatorAddress, primeAddrs...); err != nil {
			return nil, fmt.Errorf("failed to prime trader account: %w", err)
		}
	}

	if len(granteeAddrs) > 0 {
		logger.Info("adding fee grant to trader accounts", zap.Strings("addresses", primeAddrs))
		if err = addFeeGrantToTrader(operatorClient, operatorName, operatorAddress, granteeAddrs...); err != nil {
			return nil, fmt.Errorf("failed to add grant to trader: %w", err)
		}
	}

	err = addTradersToGroup(operatorName, operatorAddress.String(), cfg.Operator.GroupID, operatorClient, activeAccs)
	if err != nil {
		return nil, err
	}

	traderList := make([]*trader, 0, len(traders))
	for _, t := range traders {
		traderList = append(traderList, t)
	}

	return &bot{
		cfg:     cfg,
		pm:      pm,
		logger:  logger,
		traders: traderList,
	}, nil
}

func (b bot) Start(ctx context.Context) {

	_ = b.pm.start(ctx)

	for _, t := range b.traders {
		go t.managePositions()
	}

	<-ctx.Done()
}
