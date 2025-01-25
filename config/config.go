package config

import (
	"log"
	"time"

	"github.com/dymensionxyz/cosmosclient/cosmosclient"
	"github.com/ignite/cli/ignite/pkg/cosmosaccount"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/viper"
)

type Config struct {
	NodeAddress  string       `mapstructure:"node_address"`
	AnalyticsURL string       `mapstructure:"analytics_url"`
	DBPath       string       `mapstructure:"db_path"`
	Gas          GasConfig    `mapstructure:"gas"`
	Whale        WhaleConfig  `mapstructure:"whale"`
	Traders      TraderConfig `mapstructure:"traders"`
	LogLevel     string       `mapstructure:"log_level"`
}

type GasConfig struct {
	Prices            string `mapstructure:"prices"`
	Fees              string `mapstructure:"fees"`
	MinimumGasBalance string `mapstructure:"minimum_gas_balance"`
}

type TraderConfig struct {
	Scale                  int                          `mapstructure:"scale"`
	TimeToScale            time.Duration                `mapstructure:"time_to_scale"`
	ScaleDelayRatio        float64                      `mapstructure:"scale_delay_ratio"`
	KeyringBackend         cosmosaccount.KeyringBackend `mapstructure:"keyring_backend"`
	KeyringDir             string                       `mapstructure:"keyring_dir"`
	PositionManageInterval time.Duration                `mapstructure:"position_manage_interval"`
	CooldownRangeMinutes   string                       `mapstructure:"cooldown_range_minutes"`
}

type WhaleConfig struct {
	AccountName                  string                       `mapstructure:"account_name"`
	KeyringBackend               cosmosaccount.KeyringBackend `mapstructure:"keyring_backend"`
	KeyringDir                   string                       `mapstructure:"keyring_dir"`
	AllowedBalanceThresholds     map[string]string            `mapstructure:"allowed_balance_thresholds"`
	NumberOfIntermediaryAccounts int                          `mapstructure:"num_intermediary_accounts"`
}

const (
	HubAddressPrefix = "dym"
	PubKeyPrefix     = "pub"

	defaultNodeAddress       = "http://localhost:36657"
	defaultAnalyticsURL      = "https://fetchanalyticsrequest-p7gld3dazq-uc.a.run.app"
	defaultLogLevel          = "info"
	defaultHubDenom          = "adym"
	defaultGasFees           = "3000000000000000" + defaultHubDenom
	defaultMinimumGasBalance = "1000000000000000000" + defaultHubDenom
	testKeyringBackend       = "test"

	TraderNamePrefix            = "trader-"
	IntermediaryPrefix          = "inter-"
	defaultOperatorAccountName  = "operator"
	defaultTraderScale          = 2
	defaultTimeToScale          = defaultTraderScale * time.Minute
	defaultScaleDelayRatio      = 5.0
	defaultIntermediaryAccounts = 1
	defaultCooldownRangeMinutes = "3-5"
)

func InitConfig() {
	// Set default values
	// Find home directory.
	home, err := homedir.Dir()
	if err != nil {
		log.Fatalf("failed to get home directory: %v", err)
	}
	defaultHomeDir := home + "/.eco-bot"

	viper.SetDefault("log_level", defaultLogLevel)
	viper.SetDefault("node_address", defaultNodeAddress)
	viper.SetDefault("analytics_url", defaultAnalyticsURL)
	viper.SetDefault("gas.fees", defaultGasFees)
	viper.SetDefault("gas.minimum_gas_balance", defaultMinimumGasBalance)

	viper.SetDefault("whale.account_name", defaultOperatorAccountName)
	viper.SetDefault("whale.keyring_backend", testKeyringBackend)
	viper.SetDefault("whale.keyring_dir", defaultHomeDir)
	viper.SetDefault("whale.num_intermediary_accounts", defaultIntermediaryAccounts)

	viper.SetDefault("traders.keyring_backend", testKeyringBackend)
	viper.SetDefault("traders.keyring_dir", defaultHomeDir)
	viper.SetDefault("traders.scale", defaultTraderScale)
	viper.SetDefault("traders.time_to_scale", defaultTimeToScale)
	viper.SetDefault("traders.scale_delay_ratio", defaultScaleDelayRatio)
	viper.SetDefault("traders.position_manage_interval", 10*time.Minute)
	viper.SetDefault("traders.cooldown_range_minutes", defaultCooldownRangeMinutes)

	viper.SetConfigType("yaml")
	if CfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(CfgFile)
	} else {
		CfgFile = defaultHomeDir + "/config.yaml"
		viper.AddConfigPath(defaultHomeDir)
		viper.AddConfigPath(".")
		viper.SetConfigName("config")
	}
}

var CfgFile string

type ClientConfig struct {
	HomeDir        string
	NodeAddress    string
	GasFees        string
	GasPrices      string
	FeeGranter     string
	KeyringBackend cosmosaccount.KeyringBackend
}

func GetCosmosClientOptions(config ClientConfig) []cosmosclient.Option {
	options := []cosmosclient.Option{
		cosmosclient.WithAddressPrefix(HubAddressPrefix),
		cosmosclient.WithHome(config.HomeDir),
		cosmosclient.WithNodeAddress(config.NodeAddress),
		cosmosclient.WithFees(config.GasFees),
		cosmosclient.WithGas(cosmosclient.GasAuto),
		cosmosclient.WithGasPrices(config.GasPrices),
		cosmosclient.WithKeyringBackend(config.KeyringBackend),
		cosmosclient.WithKeyringDir(config.HomeDir),
		cosmosclient.WithFeeGranter(config.FeeGranter),
	}
	return options
}
