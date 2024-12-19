package config

import (
	"log"

	"github.com/dymensionxyz/cosmosclient/cosmosclient"
	"github.com/ignite/cli/ignite/pkg/cosmosaccount"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/viper"
)

type Config struct {
	NodeAddress string         `mapstructure:"node_address"`
	Gas         GasConfig      `mapstructure:"gas"`
	Operator    OperatorConfig `mapstructure:"operator"`
	Traders     TraderConfig   `mapstructure:"traders"`
	LogLevel    string         `mapstructure:"log_level"`
}

type GasConfig struct {
	Prices string `mapstructure:"prices"`
	Fees   string `mapstructure:"fees"`
}

type TraderConfig struct {
	Scale          int                          `mapstructure:"scale"`
	PolicyAddress  string                       `mapstructure:"policy_address"`
	KeyringBackend cosmosaccount.KeyringBackend `mapstructure:"keyring_backend"`
	KeyringDir     string                       `mapstructure:"keyring_dir"`
}

type OperatorConfig struct {
	AccountName    string                       `mapstructure:"account_name"`
	KeyringBackend cosmosaccount.KeyringBackend `mapstructure:"keyring_backend"`
	KeyringDir     string                       `mapstructure:"keyring_dir"`
	GroupID        int                          `mapstructure:"group_id"`
}

const (
	HubAddressPrefix = "dym"
	PubKeyPrefix     = "pub"

	defaultNodeAddress = "http://localhost:36657"
	defaultLogLevel    = "info"
	defaultHubDenom    = "adym"
	defaultGasFees     = "3000000000000000" + defaultHubDenom
	testKeyringBackend = "test"

	TraderNamePrefix           = "trader-"
	defaultOperatorAccountName = "operator"
	defaultTraderScale         = 20
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
	viper.SetDefault("gas.fees", defaultGasFees)

	viper.SetDefault("operator.account_name", defaultOperatorAccountName)
	viper.SetDefault("operator.keyring_backend", testKeyringBackend)
	viper.SetDefault("operator.keyring_dir", defaultHomeDir)

	viper.SetDefault("traders.keyring_backend", testKeyringBackend)
	viper.SetDefault("traders.keyring_dir", defaultHomeDir)
	viper.SetDefault("traders.scale", defaultTraderScale)
	viper.SetDefault("traders.policy_address", "<your-policy-address>")

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
