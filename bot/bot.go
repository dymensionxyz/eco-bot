package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/dymensionxyz/cosmosclient/cosmosclient"
	"go.uber.org/zap"

	"github.com/dymensionxyz/eco-bot/config"
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
	q := newQuerier(pmClient, cfg.AnalyticsURL, logger)
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
	cooldownRangeMinutes []int,
	maxPositions int,
	topUpCh chan topUpRequest,
	q querier,
	iteratePlans func(f iteratePlanCallback) error,
) (*trader, error) {
	if len(cooldownRangeMinutes) != 2 {
		return nil, fmt.Errorf("cooldown range must have 2 values")
	}

	if cooldownRangeMinutes[0] > cooldownRangeMinutes[1] {
		return nil, fmt.Errorf("cooldown range is invalid")
	}

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
		cooldownRangeMinutes,
		maxPositions,
		logger,
	)

	return t, nil
}

type botState struct {
	LastScaledTraderIdx int `json:"last_scaled_trader_idx"`
}

func (b *botState) saveState(dir string) error {
	jsn, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	if err := os.WriteFile(fmt.Sprintf("%s/state.json", dir), jsn, 0644); err != nil {
		return fmt.Errorf("failed to write state to file: %w", err)
	}

	return nil
}

func (b *botState) loadState(dir string) error {
	jsn, err := os.ReadFile(fmt.Sprintf("%s/state.json", dir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if err := b.saveState(dir); err != nil {
				return fmt.Errorf("failed to save state: %w", err)
			}
			return nil
		} else {
			return fmt.Errorf("failed to read state from file: %w", err)
		}
	}

	if err := json.Unmarshal(jsn, b); err != nil {
		return fmt.Errorf("failed to unmarshal state: %w", err)
	}

	return nil
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

	cClient, err := cosmosclient.New(config.GetCosmosClientOptions(traderClientCfg)...)
	if err != nil {
		b.logger.Error("failed to create cosmos client for intermediary accounts", zap.Error(err))
		return
	}

	accs, err := scaleIntermediaryAccounts(b.cfg.Whale.NumberOfIntermediaryAccounts, cClient, b.logger)
	if err != nil {
		b.logger.Error("failed to add intermediary accounts", zap.Error(err))
		return
	}

	for ia := range b.cfg.Whale.NumberOfIntermediaryAccounts {
		cClient, err := cosmosclient.New(config.GetCosmosClientOptions(traderClientCfg)...)
		if err != nil {
			b.logger.Error(
				"failed to create cosmos client for intermediary account",
				zap.Int("index", ia),
				zap.Error(err))
			continue
		}

		acc := accs[ia]

		as, err := newAccountService(
			cClient,
			b.logger,
			acc.Name,
			minGasBalance,
			nil,
		)
		if err != nil {
			b.logger.Error(
				"failed to create account service for intermediary account",
				zap.Int("index", ia),
				zap.Error(err))
			continue
		}

		b.whale.addIntermediaryAccount(as)
	}

	// save state to file
	st := &botState{}
	if err := st.loadState(b.cfg.Traders.KeyringDir); err != nil {
		b.logger.Error("failed to load state", zap.Error(err))
		return
	}

	lastScaledIdx := st.LastScaledTraderIdx
	cooldownRangeMinutes := parseCooldownRange(b.cfg.Traders.CooldownRangeMinutes)
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
			b.logger.Error(
				"failed to create cosmos client for trader",
				zap.Int("index", traderIdx),
				zap.Error(err))
			errored++
			continue
		}

		scale := traderIdx + 1 - errored

		accs, err := scaleTraderAccounts(scale, cClient, b.logger)
		if err != nil {
			b.logger.Error(
				"failed to add trader accounts",
				zap.Int("index", traderIdx),
				zap.Error(err))
			errored++
			continue
		}

		acc := accs[traderIdx]

		b.logger.Debug(
			fmt.Sprintf("Trader %d starting", traderIdx),
			zap.String("name", acc.Name),
			zap.String("address", acc.Address))

		if _, ok := traders[acc.Name]; ok {
			b.logger.Error(
				"trader already exists",
				zap.Int("index", traderIdx),
				zap.String("account", acc.Name))
			errored++
			continue
		}

		maxPositions := 10 - traderIdx%9

		t, err := b.addTrader(
			b.cfg.Traders.KeyringDir,
			b.cfg.Traders.PositionManageInterval,
			b.logger,
			acc.Name,
			cClient,
			minGasBalance,
			cooldownRangeMinutes,
			maxPositions,
			b.topUpCh,
			b.querier,
			b.pm.iteratePlans,
		)
		if err != nil {
			b.logger.Error(
				"failed to create trader",
				zap.String("account", acc.Name),
				zap.String("address", acc.Address),
				zap.Int("index", traderIdx),
				zap.Error(err))
			errored++
			continue
		}
		traders[acc.Name] = t

		b.logger.Info(
			"trader added",
			zap.String("account", acc.Name),
			zap.String("address", acc.Address))

		go t.managePositions()

		st.LastScaledTraderIdx = traderIdx
		if err := st.saveState(b.cfg.Traders.KeyringDir); err != nil {
			b.logger.Error("failed to save state", zap.Error(err))
		}

		if traderIdx == b.cfg.Traders.Scale-1 {
			break
		}

		delaySeconds := a * math.Pow(r, float64(traderIdx-1))
		if traderIdx < lastScaledIdx {
			delaySeconds = 0
		}
		delay := time.Duration(delaySeconds) * time.Second

		b.logger.Debug(fmt.Sprintf("Trader %d will start in %v seconds", traderIdx+1, delaySeconds))

		// sleep before starting the i-th trader
		time.Sleep(delay)
		cumulative += delay
	}

	b.logger.Debug(fmt.Sprintf("Total time used to scale up traders ~ %v", cumulative))

	<-ctx.Done()
}

func parseCooldownRange(cooldownRange string) []int {
	var cooldownRangeMinutes []int
	for _, v := range strings.Split(cooldownRange, "-") {
		i, err := strconv.Atoi(v)
		if err != nil {
			return []int{0, 0}
		}
		cooldownRangeMinutes = append(cooldownRangeMinutes, i)
	}
	return cooldownRangeMinutes
}
