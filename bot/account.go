package bot

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/cosmos-sdk/x/auth/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/dymensionxyz/cosmosclient/cosmosclient"
	"github.com/google/uuid"
	"github.com/ignite/cli/ignite/pkg/cosmosaccount"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"github.com/dymensionxyz/eco-bot/config"
)

type accountService struct {
	client            cosmosClient
	bankClient        bankClient
	accountRegistry   cosmosaccount.Registry
	logger            *zap.Logger
	account           client.Account
	balances          sdk.Coins
	minimumGasBalance sdk.Coin
	accountName       string
	homeDir           string
	topUpFactor       int
	topUpCh           chan<- topUpRequest
	asyncClient       bool
}

type bankClient interface {
	SpendableBalances(ctx context.Context, in *banktypes.QuerySpendableBalancesRequest, opts ...grpc.CallOption) (*banktypes.QuerySpendableBalancesResponse, error)
}

type option func(*accountService)

func withTopUpFactor(topUpFactor int) option {
	return func(s *accountService) {
		s.topUpFactor = topUpFactor
	}
}

func newAccountService(
	client cosmosclient.Client,
	logger *zap.Logger,
	accountName string,
	minimumGasBalance sdk.Coin,
	topUpCh chan topUpRequest,
	options ...option,
) (*accountService, error) {
	a := &accountService{
		client:            client,
		accountRegistry:   client.AccountRegistry,
		logger:            logger.With(zap.String("module", "account-service"), zap.String("account", accountName)),
		accountName:       accountName,
		homeDir:           client.Context().HomeDir,
		bankClient:        banktypes.NewQueryClient(client.Context()),
		minimumGasBalance: minimumGasBalance,
		topUpCh:           topUpCh,
	}

	for _, opt := range options {
		opt(a)
	}

	if err := a.setupAccount(); err != nil {
		return nil, fmt.Errorf("failed to setup account: %w", err)
	}

	return a, a.refreshBalances(context.Background())
}

func (a *accountService) setupAccount() error {
	acc, err := a.accountRegistry.GetByName(a.accountName)
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}
	a.account = mustConvertAccount(acc.Record)

	a.logger.Debug("using account",
		zap.String("name", a.accountName),
		zap.String("pub key", a.account.GetPubKey().String()),
		zap.String("address", a.account.GetAddress().String()),
		zap.String("keyring-backend", a.accountRegistry.Keyring.Backend()),
		zap.String("home-dir", a.homeDir),
	)
	return nil
}

func (a *accountService) address() string {
	return a.account.GetAddress().String()
}

func (a *accountService) getAccountBalances(ctx context.Context) (sdk.Coins, error) {
	resp, err := a.bankClient.SpendableBalances(ctx, &banktypes.QuerySpendableBalancesRequest{
		Address: a.address(),
	})
	if err != nil {
		return nil, err
	}
	return resp.Balances, nil
}

func (a *accountService) ensureBalances(ctx context.Context, coins sdk.Coins) ([]string, error) {
	// check if gas balance is below minimum
	gasDiff := math.NewInt(0)
	if !a.minimumGasBalance.IsNil() && a.minimumGasBalance.IsPositive() {
		gasBalance := a.balanceOf(a.minimumGasBalance.Denom)
		gasDiff = a.minimumGasBalance.Amount.Sub(gasBalance)
	}

	toTopUp := sdk.NewCoins()
	if gasDiff.IsPositive() {
		toTopUp = toTopUp.Add(a.minimumGasBalance) // add the whole amount instead of the difference
	}

	fundedDenoms := make([]string, 0, len(coins))

	// check if balance is below required
	for _, coin := range coins {
		balance := a.balanceOf(coin.Denom)
		diff := coin.Amount.Sub(balance)
		if diff.IsPositive() {
			// add x times the coin amount to the top up
			// to avoid frequent top ups
			if a.topUpFactor > 0 {
				coin.Amount = coin.Amount.MulRaw(int64(a.topUpFactor))
			}
			toTopUp = toTopUp.Add(coin) // add the whole amount instead of the difference
		} else {
			fundedDenoms = append(fundedDenoms, coin.Denom)
		}
	}

	if toTopUp.Empty() {
		return fundedDenoms, nil
	}

	// blocking operation
	resCh := make(chan []string)
	topUpReq := topUpRequest{
		coins:  toTopUp,
		toAddr: a.account.GetAddress().String(),
		res:    resCh,
	}

	a.topUpCh <- topUpReq
	res := <-resCh
	a.logger.Debug("topped up denoms", zap.Strings("denoms", res))
	close(resCh)

	if err := a.refreshBalances(ctx); err != nil {
		return nil, fmt.Errorf("failed to refresh account balances: %w", err)
	}

	for _, coin := range coins {
		balance := a.balanceOf(coin.Denom)
		if balance.GTE(coin.Amount) {
			fundedDenoms = append(fundedDenoms, coin.Denom)
		}
	}

	return fundedDenoms, nil
}

func (a *accountService) sendCoins(ctx context.Context, coins sdk.Coins, toAddrStr string) error {
	toAddr, err := sdk.AccAddressFromBech32(toAddrStr)
	if err != nil {
		return fmt.Errorf("failed to parse address: %w", err)
	}

	msg := banktypes.NewMsgSend(
		a.account.GetAddress(),
		toAddr,
		coins,
	)
	start := time.Now()

	rsp, err := a.client.BroadcastTx(a.accountName, msg)
	if err != nil {
		return fmt.Errorf("failed to broadcast tx: %w", err)
	}

	if _, err = waitForTx(a.client, rsp.TxHash); err != nil {
		return fmt.Errorf("failed to wait for tx: %w", err)
	}

	if err := a.refreshBalances(ctx); err != nil {
		a.logger.Error("failed to refresh account balances", zap.Error(err))
	}

	a.logger.Debug("coins sent", zap.String("to", toAddrStr), zap.Duration("duration", time.Since(start)))

	return nil
}

func (a *accountService) refreshBalances(ctx context.Context) error {
	balances, err := a.getAccountBalances(ctx)
	if err != nil {
		return fmt.Errorf("failed to get account balances: %w", err)
	}
	a.balances = balances
	return nil
}

func (a *accountService) balanceOf(denom string) sdk.Int {
	if a.balances == nil {
		return sdk.ZeroInt()
	}
	return a.balances.AmountOf(denom)
}

func addAccount(client cosmosclient.Client, name string) (string, error) {
	acc, err := client.AccountRegistry.GetByName(name)
	if err == nil {
		address, err := acc.Record.GetAddress()
		if err != nil {
			return "", fmt.Errorf("failed to get account address: %w", err)
		}
		return address.String(), nil
	}

	acc, _, err = client.AccountRegistry.Create(name)
	if err != nil {
		return "", fmt.Errorf("failed to create account: %w", err)
	}

	address, err := acc.Record.GetAddress()
	if err != nil {
		return "", fmt.Errorf("failed to get account address: %w", err)
	}
	return address.String(), nil
}

func getTraderAccounts(client cosmosclient.Client) (accs []account, err error) {
	var accounts []account
	accounts, err = listAccounts(client)
	if err != nil {
		return
	}

	for _, acc := range accounts {
		if !strings.HasPrefix(acc.Name, config.TraderNamePrefix) {
			continue
		}
		accs = append(accs, acc)
	}
	return
}

func getIntermediaryAccounts(client cosmosclient.Client) (accs []account, err error) {
	var accounts []account
	accounts, err = listAccounts(client)
	if err != nil {
		return
	}

	for _, acc := range accounts {
		if !strings.HasPrefix(acc.Name, config.IntermediaryPrefix) {
			continue
		}
		accs = append(accs, acc)
	}
	return
}

func listAccounts(client cosmosclient.Client) ([]account, error) {
	accs, err := client.AccountRegistry.List()
	if err != nil {
		return nil, fmt.Errorf("failed to get accounts: %w", err)
	}

	var accounts []account
	for _, acc := range accs {
		addr, err := acc.Record.GetAddress()
		if err != nil {
			return nil, fmt.Errorf("failed to get account address: %w", err)
		}
		acct := account{
			Name:    acc.Name,
			Address: addr.String(),
		}
		accounts = append(accounts, acct)
	}

	return accounts, nil
}

func createTraderAccounts(client cosmosclient.Client, count int) (accs []account, err error) {
	for range count {
		traderName := fmt.Sprintf("%s%s", config.TraderNamePrefix, uuid.New().String()[0:5])
		addr, err := addAccount(client, traderName)
		if err != nil {
			return nil, fmt.Errorf("failed to create account: %w", err)
		}
		acc := account{
			Name:    traderName,
			Address: addr,
		}
		accs = append(accs, acc)
	}
	return
}

func createIntermediaryAccounts(client cosmosclient.Client, count int) (accs []account, err error) {
	for range count {
		intermediaryName := fmt.Sprintf("%s%s", config.IntermediaryPrefix, uuid.New().String()[0:5])
		addr, err := addAccount(client, intermediaryName)
		if err != nil {
			return nil, fmt.Errorf("failed to create account: %w", err)
		}
		acc := account{
			Name:    intermediaryName,
			Address: addr,
		}
		accs = append(accs, acc)
	}
	return
}

func waitForTx(client cosmosClient, txHash string) (*tx.GetTxResponse, error) {
	serviceClient := tx.NewServiceClient(client.Context())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	ticker := time.NewTicker(time.Second)

	defer func() {
		cancel()
		ticker.Stop()
	}()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out waiting for tx %s", txHash)
		case <-ticker.C:
			resp, err := serviceClient.GetTx(ctx, &tx.GetTxRequest{Hash: txHash})
			if err != nil {
				continue
			}
			if resp.TxResponse.Code == 0 {
				return resp, nil
			} else {
				return nil, fmt.Errorf("tx failed with code %d: %s", resp.TxResponse.Code, resp.TxResponse.RawLog)
			}
		}
	}
}

func scaleTraderAccounts(scale int, cClient cosmosclient.Client, logger *zap.Logger) ([]account, error) {
	accs, err := getTraderAccounts(cClient)
	if err != nil {
		return nil, fmt.Errorf("failed to get trader accounts: %w", err)
	}

	numFoundTraders := len(accs)

	tradersAccountsToCreate := max(0, scale) - numFoundTraders
	if tradersAccountsToCreate <= 0 {
		return accs, nil
	}

	logger.Info("creating trader accounts", zap.Int("accounts", tradersAccountsToCreate))

	newAccs, err := createTraderAccounts(cClient, tradersAccountsToCreate)
	if err != nil {
		return nil, fmt.Errorf("failed to create trader accounts: %w", err)
	}

	accs = slices.Concat(accs, newAccs)

	if len(accs) < scale {
		return nil, fmt.Errorf("expected %d trader accounts, got %d", scale, len(accs))
	}
	return accs, nil
}

func scaleIntermediaryAccounts(scale int, cClient cosmosclient.Client, logger *zap.Logger) ([]account, error) {
	accs, err := getIntermediaryAccounts(cClient)
	if err != nil {
		return nil, fmt.Errorf("failed to get intermediary accounts: %w", err)
	}

	numFoundIntermediaries := len(accs)

	intermediaryAccountsToCreate := max(0, scale) - numFoundIntermediaries
	if intermediaryAccountsToCreate <= 0 {
		return accs, nil
	}

	logger.Info("creating intermediary accounts", zap.Int("accounts", intermediaryAccountsToCreate))

	newAccs, err := createIntermediaryAccounts(cClient, intermediaryAccountsToCreate)
	if err != nil {
		return nil, fmt.Errorf("failed to create intermediary accounts: %w", err)
	}

	accs = slices.Concat(accs, newAccs)

	if len(accs) < scale {
		return nil, fmt.Errorf("expected %d intermediary accounts, got %d", scale, len(accs))
	}
	return accs, nil
}

func accountExists(clientCtx client.Context, address string) (bool, error) {
	// Parse the address
	addr, err := sdk.AccAddressFromBech32(address)
	if err != nil {
		return false, fmt.Errorf("invalid address: %v", err)
	}

	// Create a query client for the auth module
	authClient := authtypes.NewQueryClient(clientCtx)

	// Prepare the request
	req := &authtypes.QueryAccountRequest{
		Address: addr.String(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Query the account
	res, err := authClient.Account(ctx, req)
	if err != nil {
		// Check if the error indicates that the account does not exist
		if grpcErrorCode(err) == "NotFound" {
			return false, nil
		}
		return false, fmt.Errorf("failed to query account: %v", err)
	}

	// If res.Account is not nil, account exists
	if res.Account != nil {
		return true, nil
	}

	return false, nil
}

func mustConvertAccount(rec *keyring.Record) client.Account {
	address, err := rec.GetAddress()
	if err != nil {
		panic(fmt.Errorf("failed to get account address: %w", err))
	}
	pubKey, err := rec.GetPubKey()
	if err != nil {
		panic(fmt.Errorf("failed to get account pubkey: %w", err))
	}
	return types.NewBaseAccount(address, pubKey, 0, 0)
}

func grpcErrorCode(err error) string {
	if grpcStatus, ok := status.FromError(err); ok {
		return grpcStatus.Code().String()
	}
	return ""
}
