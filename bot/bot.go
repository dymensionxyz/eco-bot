package bot

import (
	"context"
	"fmt"
	"math"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/dymensionxyz/cosmosclient/cosmosclient"
	"go.uber.org/zap"

	"github.com/dymensionxyz/eco-bot/config"
	"github.com/dymensionxyz/eco-bot/types"
)

type bot struct {
	cfg     config.Config
	pm      *positionManager
	querier querier
	whale   *whale
	topUpCh chan topUpRequest

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

	return &bot{
		cfg:     cfg,
		pm:      pm,
		querier: q,
		whale:   whaleSvc,
		topUpCh: topUpCh,
		logger:  logger,
	}, nil
}

func (b bot) addTrader(
	keyringDir string,
	posManageInterval time.Duration,
	logger *zap.Logger,
	accName string,
	cClient cosmosclient.Client,
	minGasBalance sdk.Coin,
	topUpCh chan topUpRequest,
	q querier,
	iteratePlans func(f func(types.Plan) error) error,
) (*trader, error) {
	as, err := newAccountService(
		cClient,
		logger,
		accName,
		minGasBalance,
		topUpCh,
		// withTopUpFactor(cfg.TopUpFactor),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create account service for bot: %s;err: %w", accName, err)
	}

	getState, setState := newStateKeeper(fmt.Sprintf("%s/state-%s.json", keyringDir, accName))

	t := newTrader(
		as,
		q,
		iteratePlans,
		cClient,
		setState,
		getState,
		posManageInterval,
		logger,
	)

	return t, nil
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

	traderClientCfg := config.ClientConfig{
		HomeDir:        b.cfg.Traders.KeyringDir,
		KeyringBackend: b.cfg.Traders.KeyringBackend,
		NodeAddress:    b.cfg.NodeAddress,
		GasFees:        b.cfg.Gas.Fees,
		GasPrices:      b.cfg.Gas.Prices,
	}

	minGasBalance, err := sdk.ParseCoinNormalized(b.cfg.Gas.MinimumGasBalance)
	if err != nil {
		b.logger.Error("failed to parse minimum gas balance", zap.Error(err))
		return
	}

	errored := 0
	traders := make(map[string]*trader)

	timeToScaleSec := b.cfg.Traders.TimeToScale.Seconds()
	r := math.Pow(1/b.cfg.Traders.ScaleDelayRatio, 1/float64(b.cfg.Traders.Scale-1))

	numerator := float64(timeToScaleSec) * (1 - r)
	denominator := 1 - math.Pow(r, float64(b.cfg.Traders.Scale))
	a := numerator / denominator

	var cumulative time.Duration

	for traderIdx := range b.cfg.Traders.Scale {
		cClient, err := cosmosclient.New(config.GetCosmosClientOptions(traderClientCfg)...)
		if err != nil {
			b.logger.Error("failed to create cosmos client for trader",
				zap.Int("index", traderIdx),
				zap.Error(err))
			errored++
			continue
		}

		scale := traderIdx + 1 - errored

		accs, err := scaleTraderAccounts(scale, cClient, b.logger)
		if err != nil {
			b.logger.Error("failed to add trader accounts",
				zap.Int("index", traderIdx),
				zap.Error(err))
			errored++
			continue
		}

		acc := accs[traderIdx]

		if _, ok := traders[acc.Name]; ok {
			b.logger.Error("trader already exists",
				zap.Int("index", traderIdx),
				zap.String("account", acc.Name))
			errored++
			continue
		}

		t, err := b.addTrader(
			b.cfg.Traders.KeyringDir,
			b.cfg.Traders.PositionManageInterval,
			b.logger,
			acc.Name,
			cClient,
			minGasBalance,
			b.topUpCh,
			b.querier,
			b.pm.iteratePlans,
		)
		if err != nil {
			b.logger.Error(
				"failed to create trader",
				zap.String("account", acc.Name),
				zap.Int("index", traderIdx),
				zap.Error(err))
			errored++
			continue
		}
		traders[acc.Name] = t

		b.logger.Info("trader added", zap.String("account", acc.Name))

		go t.managePositions()

		delaySeconds := a * math.Pow(r, float64(traderIdx-1))
		delay := time.Duration(delaySeconds) * time.Second

		fmt.Printf("Trader %d will start in %v seconds\n", traderIdx+1, delaySeconds)

		// sleep before starting the i-th trader
		time.Sleep(delay)
		cumulative += delay
	}

	fmt.Printf("Total time used ~ %v\n", cumulative)

	<-ctx.Done()
}
