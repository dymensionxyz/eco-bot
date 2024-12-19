package cmd

import (
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/dymensionxyz/eco-bot/bot"
	"github.com/dymensionxyz/eco-bot/config"
	"github.com/dymensionxyz/eco-bot/version"
)

var RootCmd = &cobra.Command{
	Use:   "eco-bot",
	Short: "ECO Bot for Dymension Hub",
	Long:  `Eco Bot for Dymension Hub that scans for new IROs and invests in them.`,
	Run: func(cmd *cobra.Command, args []string) {
		// If no arguments are provided, print usage information
		if len(args) == 0 {
			if err := cmd.Usage(); err != nil {
				log.Fatalf("Error printing usage: %v", err)
			}
		}
	},
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the eco bot",
	Long:  `Initialize the eco bot by generating a config file with default values.`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := config.Config{}
		if err := viper.Unmarshal(&cfg); err != nil {
			log.Fatalf("failed to unmarshal config: %v", err)
		}

		// if trader key dir doesn't exist, create it
		if _, err := os.Stat(cfg.Traders.KeyringDir); os.IsNotExist(err) {
			if err := os.MkdirAll(cfg.Traders.KeyringDir, 0o755); err != nil {
				log.Fatalf("failed to create trader key directory: %v", err)
			}
		}

		if err := viper.WriteConfigAs(config.CfgFile); err != nil {
			log.Fatalf("failed to write config file: %v", err)
		}

		fmt.Printf("Config file created: %s\n", config.CfgFile)
		fmt.Println()
		fmt.Println("Edit the config file to set the correct values for your environment.")
	},
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the eco bot",
	Long:  `Start the eco bot that scans for new IROs and invests in them.`,
	Run: func(cmd *cobra.Command, args []string) {
		viper.AutomaticEnv()

		if err := viper.ReadInConfig(); err == nil {
			fmt.Println("Using config file:", viper.ConfigFileUsed())
		}

		cfg := config.Config{}
		if err := viper.Unmarshal(&cfg); err != nil {
			log.Fatalf("failed to unmarshal config: %v", err)
		}

		log.Printf("using config file: %+v", viper.ConfigFileUsed())

		logger, err := buildLogger(cfg.LogLevel)
		if err != nil {
			log.Fatalf("failed to build logger: %v", err)
		}

		// Ensure all logs are written
		defer logger.Sync() // nolint: errcheck

		oc, err := bot.NewBot(cfg, logger)
		if err != nil {
			log.Fatalf("failed to create eco bot: %v", err)
		}

		if cfg.Traders.Scale == 0 {
			log.Println("no traders to start")
			return
		}

		oc.Start(cmd.Context())
	},
}

func buildLogger(logLevel string) (*zap.Logger, error) {
	var level zapcore.Level
	if err := level.Set(logLevel); err != nil {
		return nil, fmt.Errorf("failed to set log level: %w", err)
	}

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	logger := zap.New(zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.Lock(os.Stdout),
		level,
	))

	return logger, nil
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version of eco-bot",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(version.BuildVersion)
	},
}

func init() {
	RootCmd.CompletionOptions.DisableDefaultCmd = true
	RootCmd.AddCommand(initCmd)
	RootCmd.AddCommand(startCmd)
	RootCmd.AddCommand(versionCmd)

	cobra.OnInitialize(config.InitConfig)

	RootCmd.PersistentFlags().StringVar(&config.CfgFile, "config", "", "config file")

	// Cobra also supports local flags, which will only run
	// when this action is called directly.
	RootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
