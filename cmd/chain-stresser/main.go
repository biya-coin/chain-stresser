package main

import (
	"context"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	ethcmn "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/xlab/closer"
	"github.com/xlab/pace"
	log "github.com/xlab/suplog"

	stresser "github.com/biya-coin/chain-stresser/v2"
	"github.com/biya-coin/chain-stresser/v2/chain"
	"github.com/biya-coin/chain-stresser/v2/deploy"
	"github.com/biya-coin/chain-stresser/v2/payload"
	"github.com/biya-coin/chain-stresser/v2/replay"
)

const (
	defaultChainID       = "stressbiya-801"
	defaultEthChainID    = 801
	defaultMinGasPrice   = "1byb"
	defaultNumOfAccounts = 1000
	defaultNumOfTx       = 100

	defaultNumOfValidators = 1
	defaultNumOfSentries   = 0
	defaultNumOfInstances  = 1

	defaultInjectiveDockerImage = "injectivelabs/injective-core"
	defaultDockerSubnet         = "172.127.0.0/24"

	latestInjectiveCoreTag = "v1.16.4"
)

var (
	verboseOutput = false
)

func init() {
	// ignore debugging stuff by default
	log.DefaultLogger.SetLevel(log.InfoLevel)
}

func main() {
	var (
		stressCfg = stresser.StressConfig{
			EthChainID:        defaultEthChainID,
			MinGasPrice:       defaultMinGasPrice,
			NumOfTransactions: defaultNumOfTx,
		}

		accountFile   string = "accounts.json"
		numOfAccounts int    = defaultNumOfAccounts
	)

	defer closer.Close()

	rootCtx, cancelFn := context.WithCancel(context.Background())
	closer.Bind(cancelFn)

	rootCmd := &cobra.Command{
		Use: "chain-stresser",

		Hidden:        true,
		SilenceErrors: true,
		SilenceUsage:  false,
		Long:          bannerStr,

		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}

	rootCmd.PersistentFlags().StringVar(&stressCfg.ChainID, "chain-id", defaultChainID, "Expected Cosmos chain ID of the chain to connect to.")
	rootCmd.PersistentFlags().Int64Var(&stressCfg.EthChainID, "eth-chain-id", defaultEthChainID, "Expected EIP-155 chain ID of the EVM.")
	rootCmd.PersistentFlags().StringVar(&stressCfg.MinGasPrice, "min-gas-price", defaultMinGasPrice, "Minimum gas price to pay for each transaction.")
	rootCmd.PersistentFlags().StringVar(&stressCfg.NodeAddress, "node-addr", "127.0.0.1:26657", "Address of a biyachaind node RPC to connect to.")
	rootCmd.PersistentFlags().StringVar(&stressCfg.GRPCAddress, "grpc-addr", "127.0.0.1:9900", "Address of a biyachaind node GRPC to connect to.")
	rootCmd.PersistentFlags().BoolVar(&stressCfg.AwaitTxConfirmation, "await", true, "Await for transaction to be included in a block.")
	rootCmd.PersistentFlags().BoolVar(&verboseOutput, "verbose", false, "Verbosely output debugging information.")
	rootCmd.PersistentFlags().StringVar(&accountFile, "accounts", "accounts.json", "Path to a JSON file containing private keys of accounts to use for stress testing.")
	rootCmd.PersistentFlags().IntVar(&numOfAccounts, "accounts-num", defaultNumOfAccounts, "Number of accounts used to benchmark the node in parallel, must not be greater than the number of keys available in account file.")
	rootCmd.PersistentFlags().IntVar(&stressCfg.NumOfTransactions, "transactions", defaultNumOfTx, "Number of transactions to allocate for each account.")

	// Rate limiting flags
	rootCmd.PersistentFlags().Float64Var(&stressCfg.RateLimit.TxPerSecond, "rate-tps", 0, "Rate limit transactions per second. Example: 200 for 200 TPS, 99.5 for fractional rates. 0 = no limit.")
	rootCmd.PersistentFlags().Uint64Var(&stressCfg.RateLimit.BytesPerSecond, "rate-bytes", 0, "Rate limit transaction bandwidth in bytes per second. Example: 50000 for 50KB/sec. 0 = no limit.")
	rootCmd.PersistentFlags().Uint64Var(&stressCfg.RateLimit.GasPerSecond, "rate-gas", 0, "Rate limit gas consumption per second. Example: 1000000 for 1M gas/sec. 0 = no limit.")
	rootCmd.PersistentFlags().IntVar(&stressCfg.RateLimit.Burst.Size, "rate-burst-size", 0, "Custom burst size (tokens). If >0, overrides default burst calculation. 0 = auto.")

	rootCmd.SetHelpCommand(&cobra.Command{
		Hidden: true,
	})

	var genEnv stresser.GeneratorEnvironment

	generateCmd := &cobra.Command{
		Use:   "generate",
		Short: "Generates all the config files required to start biyachaind cluster with state for stress testing.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if verboseOutput {
				log.DefaultLogger.SetLevel(log.DebugLevel)
			}

			if genEnv.LocalNative {
				if genEnv.NumOfValidators > 4 {
					return errors.New("number of validators must be less than or equal to 4 when running natively")
				}
				if genEnv.NumOfSentryNodes > 0 {
					return errors.New("number of sentry nodes must be 0 when running natively")
				}
			}

			stresser.GenerateConfigs(genEnv)
			return nil
		},
	}

	generateCmd.Flags().StringVar(&genEnv.ChainID, "chain-id", defaultChainID, "Cosmos chain ID of the chain to generate.")
	generateCmd.Flags().IntVar(&genEnv.EthChainID, "eth-chain-id", defaultEthChainID, "EIP-155 chain ID of the EVM (can be different from the Cosmos chain-id).")
	generateCmd.Flags().BoolVar(&genEnv.EvmEnabled, "evm", true, "Enabled EVM support. Generates genesis with EVM state.")
	generateCmd.Flags().BoolVar(&genEnv.ProdLike, "prod", false, "Generate config for prod-like chain (app/bft configs will be close to mainnet versions).")
	generateCmd.Flags().BoolVar(&genEnv.Debug, "debug", false, "Additional debug output when running in Docker Compose mode.")
	generateCmd.Flags().StringVar(&genEnv.DockerImage, "docker-image", defaultInjectiveDockerImage+":"+latestInjectiveCoreTag, "Docker image to use for the local network via docker-compose.")
	generateCmd.Flags().StringVar(&genEnv.DockerSubnet, "docker-subnet", defaultDockerSubnet, "Docker subnet to use for the local network via docker-compose.")
	generateCmd.Flags().IntVar(&genEnv.NumOfValidators, "validators", defaultNumOfValidators, "Number of validators to generate config for.")
	generateCmd.Flags().IntVar(&genEnv.NumOfSentryNodes, "sentries", defaultNumOfSentries, "Number of sentry nodes to generate config for.")
	generateCmd.Flags().IntVar(&genEnv.NumOfInstances, "instances", defaultNumOfInstances, "The maximum number of parallel chain-stresser instances to be prepared for.")
	generateCmd.Flags().BoolVar(&genEnv.LocalNative, "native", false, "Generate config compatible with running multiple binaries natively on the host machine (no docker-compose).")
	generateCmd.Flags().IntVar(&genEnv.NumOfAccountsPerInstance, "accounts-num", defaultNumOfAccounts, "Number of funded accounts to generate for each instance.")
	generateCmd.Flags().StringVar(&genEnv.OutDirectory, "out", strOrPanic(os.Getwd()), "Path to the directory where generated files are stored.")
	rootCmd.AddCommand(generateCmd)

	txBankSendCmd := &cobra.Command{
		Use:   "tx-bank-send",
		Short: "Run stresstest with x/bank.MsgSend transactions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if verboseOutput {
				log.DefaultLogger.SetLevel(log.DebugLevel)
			}

			orPanic(readAccounts(&stressCfg, accountFile, numOfAccounts))

			sendAmount := "1" + chain.DefaultBondDenom
			bankSendProvider, err := payload.NewBankSendProvider(stressCfg.MinGasPrice, sendAmount)
			if err != nil {
				return errors.Wrap(err, "failed to initate bank send stress provider")
			}

			if err := stresser.Stress(rootCtx, stressCfg, bankSendProvider); err != nil {
				log.Errorf("❌ benchmark failed:\n\n%s", err)
				os.Exit(-1)
			}

			return nil
		},
	}
	rootCmd.AddCommand(txBankSendCmd)

	var (
		multiSendNumTargets int
	)

	txBankMultiSendCmd := &cobra.Command{
		Use:   "tx-bank-send-many",
		Short: "Run stresstest with x/bank.MsgMultiSend transactions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if verboseOutput {
				log.DefaultLogger.SetLevel(log.DebugLevel)
			}

			orPanic(readAccounts(&stressCfg, accountFile, numOfAccounts))

			sendAmount := "1" + chain.DefaultBondDenom
			bankMultiSendProvider, err := payload.NewBankMultiSendProvider(stressCfg.MinGasPrice, sendAmount, multiSendNumTargets)
			if err != nil {
				return errors.Wrap(err, "failed to initate bank multi send stress provider")
			}

			if err := stresser.Stress(rootCtx, stressCfg, bankMultiSendProvider); err != nil {
				log.Errorf("❌ benchmark failed:\n\n%s", err)
				os.Exit(-1)
			}

			return nil
		},
	}
	txBankMultiSendCmd.Flags().IntVar(&multiSendNumTargets, "targets", 50, "Number of targets to send the funds to.")
	rootCmd.AddCommand(txBankMultiSendCmd)

	txEthSendCmd := &cobra.Command{
		Use:   "tx-eth-send",
		Short: "Run stresstest with eth value send transactions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if verboseOutput {
				log.DefaultLogger.SetLevel(log.DebugLevel)
			}

			orPanic(readAccounts(&stressCfg, accountFile, numOfAccounts))

			sendAmount := "1" + chain.DefaultBondDenom
			ethSendProvider, err := payload.NewEthSendProvider(big.NewInt(stressCfg.EthChainID), stressCfg.MinGasPrice, sendAmount)
			if err != nil {
				return errors.Wrap(err, "failed to initate eth value send stress provider")
			}

			if err := stresser.Stress(rootCtx, stressCfg, ethSendProvider); err != nil {
				log.Errorf("❌ benchmark failed:\n\n%s", err)
				os.Exit(-1)
			}

			return nil
		},
	}
	rootCmd.AddCommand(txEthSendCmd)

	txEthCallCmd := &cobra.Command{
		Use:   "tx-eth-call",
		Short: "Run stresstest with eth contract call transactions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if verboseOutput {
				log.DefaultLogger.SetLevel(log.DebugLevel)
			}

			orPanic(readAccounts(&stressCfg, accountFile, numOfAccounts))

			ethCallProvider, err := payload.NewEthCallProvider(big.NewInt(stressCfg.EthChainID), stressCfg.MinGasPrice)
			if err != nil {
				return errors.Wrap(err, "failed to initate eth contract call stress provider")
			}

			if err := stresser.Stress(rootCtx, stressCfg, ethCallProvider); err != nil {
				log.Errorf("❌ benchmark failed:\n\n%s", err)
				os.Exit(-1)
			}

			return nil
		},
	}
	rootCmd.AddCommand(txEthCallCmd)

	txEthDeployCmd := &cobra.Command{
		Use:   "tx-eth-deploy",
		Short: "Run stresstest with eth contract deploy transactions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if verboseOutput {
				log.DefaultLogger.SetLevel(log.DebugLevel)
			}

			orPanic(readAccounts(&stressCfg, accountFile, numOfAccounts))

			ethDeployProvider, err := payload.NewEthDeployProvider(big.NewInt(stressCfg.EthChainID), stressCfg.MinGasPrice)
			if err != nil {
				return errors.Wrap(err, "failed to initate eth contract deploy stress provider")
			}

			if err := stresser.Stress(rootCtx, stressCfg, ethDeployProvider); err != nil {
				log.Errorf("❌ benchmark failed:\n\n%s", err)
				os.Exit(-1)
			}

			return nil
		},
	}
	rootCmd.AddCommand(txEthDeployCmd)

	var (
		erc20ContractAddr          string
		recipientAddr              string
		ethRPCURL                  string
		erc20EntrypointAddress     string
		erc20BeneficiaryAddress    string
		erc20AccountFactoryAddress string
		contractsEnvFile           string
	)

	txEthERC20UserOpCmd := &cobra.Command{
		Use:   "tx-eth-erc20-userop",
		Short: "Run stresstest with ERC20 token transfer UserOp transactions (bundled via EntryPoint).",
		RunE: func(cmd *cobra.Command, args []string) error {
			if verboseOutput {
				log.DefaultLogger.SetLevel(log.DebugLevel)
			}

			// 从 build/erc20_contracts.env 读取合约地址（如果没有通过参数指定）
			if contractsEnvFile == "" {
				projectRoot, err := getProjectRoot()
				if err == nil {
					contractsEnvFile = filepath.Join(projectRoot, "build", "erc20_contracts.env")
				}
			}

			if erc20ContractAddr == "" || erc20EntrypointAddress == "" || erc20AccountFactoryAddress == "" {
				if contractsEnvFile != "" {
					result, err := deploy.LoadFromFile(contractsEnvFile)
					if err == nil {
						if erc20ContractAddr == "" {
							erc20ContractAddr = result.TokenAddr.Hex()
						}
						if erc20EntrypointAddress == "" {
							erc20EntrypointAddress = result.EntryPointAddr.Hex()
						}
						if erc20AccountFactoryAddress == "" {
							erc20AccountFactoryAddress = result.FactoryAddr.Hex()
						}
						log.Infof("✓ 已从 %s 读取合约配置", contractsEnvFile)
					}
				}
			}

			// 验证必需参数
			if erc20ContractAddr == "" {
				return errors.New("❌ 错误: 缺少 ERC20 合约地址\n请先运行: make eth-erc20-setup\n或使用 --erc20-address 参数")
			}
			if erc20EntrypointAddress == "" {
				return errors.New("❌ 错误: 缺少 EntryPoint 合约地址\n请先运行: make eth-erc20-setup\n或使用 --entrypoint-address 参数")
			}
			if erc20AccountFactoryAddress == "" {
				return errors.New("❌ 错误: 缺少 Factory 合约地址\n请先运行: make eth-erc20-setup\n或使用 --factory-address 参数")
			}
			if recipientAddr == "" {
				return errors.New("--recipient-address is required")
			}

			// 打印配置信息
			log.Info("步骤: 运行 ERC20 UserOp 压测 (通过 EntryPoint 捆绑交易)...")
			log.Info("==========================================")
			log.Infof("ERC20 Token:  %s", erc20ContractAddr)
			log.Infof("EntryPoint:   %s", erc20EntrypointAddress)
			log.Infof("Factory:      %s", erc20AccountFactoryAddress)
			log.Infof("接收地址:     %s", recipientAddr)
			log.Infof("账户数:       %d", numOfAccounts)
			log.Infof("每账户交易:   %d", stressCfg.NumOfTransactions)
			log.Info("")

			// Debug: 验证地址是否正确
			log.Debugf("[DEBUG] erc20EntrypointAddress 变量值: %s", erc20EntrypointAddress)
			log.Debugf("[DEBUG] erc20AccountFactoryAddress 变量值: %s", erc20AccountFactoryAddress)
			log.Debugf("[DEBUG] erc20ContractAddr 变量值: %s", erc20ContractAddr)

			// 查询压测前余额
			client, err := ethclient.Dial(ethRPCURL)
			if err != nil {
				log.Warningf("连接 RPC 失败，跳过余额查询: %v", err)
			} else {
				balanceBefore, err := queryERC20Balance(client, erc20ContractAddr, recipientAddr)
				if err != nil {
					log.Warningf("查询压测前余额失败: %v", err)
				} else {
					log.Infof("压测前接收地址余额: %s tokens", formatTokenAmount(balanceBefore))
				}
				log.Info("")
			}

			orPanic(readAccounts(&stressCfg, accountFile, numOfAccounts))

			userOpsSignedPace := pace.New("userops signed", 1*time.Minute, stresser.NewPaceReporter(log.DefaultLogger))

			ethERC20UserOpProvider, err := payload.NewEthERC20UserOpProvider(
				ethRPCURL,
				big.NewInt(int64(stressCfg.EthChainID)),
				stressCfg.MinGasPrice,
				userOpsSignedPace,
				ethcmn.HexToAddress(erc20EntrypointAddress),
				ethcmn.HexToAddress(erc20BeneficiaryAddress),
				ethcmn.HexToAddress(erc20AccountFactoryAddress),
				ethcmn.HexToAddress(erc20ContractAddr),
				ethcmn.HexToAddress(recipientAddr),
			)
			if err != nil {
				return errors.Wrap(err, "failed to initiate eth erc20 userop stress provider")
			}

			if err := stresser.Stress(rootCtx, stressCfg, ethERC20UserOpProvider); err != nil {
				log.Errorf("❌ benchmark failed:\n\n%s", err)
				os.Exit(-1)
			}

			// 查询压测后余额
			log.Info("")
			if client != nil {
				balanceAfter, err := queryERC20Balance(client, erc20ContractAddr, recipientAddr)
				if err != nil {
					log.Warningf("查询压测后余额失败: %v", err)
				} else {
					log.Infof("压测后接收地址余额: %s tokens", formatTokenAmount(balanceAfter))
				}
				client.Close()
			}
			log.Info("")

			return nil
		},
	}
	txEthERC20UserOpCmd.Flags().StringVar(&erc20ContractAddr, "erc20-address", "", "ERC20 token contract address (如不提供，从 build/erc20_contracts.env 读取)")
	txEthERC20UserOpCmd.Flags().StringVar(&recipientAddr, "recipient-address", "0x0000000000000000000000000000000000000001", "Recipient address for token transfers")
	txEthERC20UserOpCmd.Flags().StringVar(&ethRPCURL, "eth-rpc-url", "http://127.0.0.1:8545", "Ethereum RPC URL")
	txEthERC20UserOpCmd.Flags().StringVar(&erc20EntrypointAddress, "entrypoint-address", "", "EntryPoint contract address (如不提供，从 build/erc20_contracts.env 读取)")
	txEthERC20UserOpCmd.Flags().StringVar(&erc20BeneficiaryAddress, "beneficiary-address", "0x0000000000000000000000000000000000000000", "Beneficiary address for UserOp fees")
	txEthERC20UserOpCmd.Flags().StringVar(&erc20AccountFactoryAddress, "factory-address", "", "Account Factory contract address (如不提供，从 build/erc20_contracts.env 读取)")
	txEthERC20UserOpCmd.Flags().StringVar(&contractsEnvFile, "contracts-env", "", "合约配置文件路径 (默认: build/erc20_contracts.env)")
	rootCmd.AddCommand(txEthERC20UserOpCmd)

	var ethInternalCallIterations uint64
	txEthInternalCallCmd := &cobra.Command{
		Use:   "tx-eth-internal-call",
		Short: "Run stresstest with eth contract internal call transactions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if verboseOutput {
				log.DefaultLogger.SetLevel(log.DebugLevel)
			}

			orPanic(readAccounts(&stressCfg, accountFile, numOfAccounts))

			ethCallProvider, err := payload.NewEthInternalCallProvider(
				big.NewInt(stressCfg.EthChainID),
				stressCfg.MinGasPrice,
				ethInternalCallIterations,
			)
			if err != nil {
				return errors.Wrap(err, "failed to initate eth contract internal call stress provider")
			}

			if err := stresser.Stress(rootCtx, stressCfg, ethCallProvider); err != nil {
				log.Errorf("❌ benchmark failed:\n\n%s", err)
				os.Exit(-1)
			}

			return nil
		},
	}
	txEthInternalCallCmd.Flags().Uint64Var(&ethInternalCallIterations, "iterations", 10, "Number of internal call iterations to run for each external tx")
	rootCmd.AddCommand(txEthInternalCallCmd)

	var (
		counterContractAddr         string
		userOpEntrypointAddress     string
		userOpBeneficiaryAddress    string
		userOpAccountFactoryAddress string
	)

	txEthUserOpCmd := &cobra.Command{
		Use:   "tx-eth-userop",
		Short: "Run stresstest with eth contract UserOp transactions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if verboseOutput {
				log.DefaultLogger.SetLevel(log.DebugLevel)
			}

			orPanic(readAccounts(&stressCfg, accountFile, numOfAccounts))

			userOpsSignedPace := pace.New("userops signed", 1*time.Minute, stresser.NewPaceReporter(log.DefaultLogger))

			ethUserOpProvider, err := payload.NewEthUserOpProvider(
				ethRPCURL,
				big.NewInt(stressCfg.EthChainID),
				stressCfg.MinGasPrice,
				userOpsSignedPace,
				ethcmn.HexToAddress(userOpEntrypointAddress),
				ethcmn.HexToAddress(userOpBeneficiaryAddress),
				ethcmn.HexToAddress(userOpAccountFactoryAddress),
				ethcmn.HexToAddress(counterContractAddr),
			)
			if err != nil {
				return errors.Wrap(err, "failed to initiate eth UserOp stress provider")
			}

			if err := stresser.Stress(rootCtx, stressCfg, ethUserOpProvider); err != nil {
				log.Errorf("❌ benchmark failed:\n\n%s", err)
				os.Exit(-1)
			}

			return nil
		},
	}

	txEthUserOpCmd.Flags().StringVar(&ethRPCURL, "eth-rpc-url", "http://127.0.0.1:8545", "Ethereum RPC URL")
	txEthUserOpCmd.Flags().StringVar(&userOpEntrypointAddress, "entrypoint-address", "0x586AaA4d77955b36784cADf6D9D617b952d45DA1", "EntryPoint contract address")
	txEthUserOpCmd.Flags().StringVar(&userOpBeneficiaryAddress, "beneficiary-address", "0x0000000000000000000000000000000000000000", "Beneficiary address for UserOp fees")
	txEthUserOpCmd.Flags().StringVar(&userOpAccountFactoryAddress, "factory-address", "0x0B3809304F2bAad3E0d0810B98Cc7e505C06ce89", "Account Factory contract address")
	txEthUserOpCmd.Flags().StringVar(&counterContractAddr, "counter-address", "0x590d9D4654FC262BFE72d115355db2aEb7DB902f", "Counter contract address")
	rootCmd.AddCommand(txEthUserOpCmd)

	var spotMarketIDs []string
	var derivativeMarketIDs []string
	var ordersPerMarket int

	txExchangeBatchOrdersCmd := &cobra.Command{
		Use:   "tx-exchange-batch-orders",
		Short: "Run stresstest with x/exchange.MsgBatchUpdateOrders transactions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if verboseOutput {
				log.DefaultLogger.SetLevel(log.DebugLevel)
			}

			orPanic(readAccounts(&stressCfg, accountFile, numOfAccounts))

			exchangeBatchOrdersProvider, err := payload.NewExchangeBatchOrdersProvider(stressCfg.MinGasPrice, spotMarketIDs, derivativeMarketIDs, ordersPerMarket)
			if err != nil {
				return errors.Wrap(err, "failed to initate exchange batch orders stress provider")
			}

			if err := stresser.Stress(rootCtx, stressCfg, exchangeBatchOrdersProvider); err != nil {
				log.Errorf("❌ benchmark failed:\n\n%s", err)
				os.Exit(-1)
			}

			return nil
		},
	}
	txExchangeBatchOrdersCmd.Flags().StringSliceVar(&spotMarketIDs, "spot-market-ids", []string{}, "Comma-separated list of spot market IDs to update.")
	txExchangeBatchOrdersCmd.Flags().StringSliceVar(&derivativeMarketIDs, "derivative-market-ids", []string{}, "Comma-separated list of derivative market IDs to update.")
	txExchangeBatchOrdersCmd.Flags().IntVar(&ordersPerMarket, "orders-per-market", 1, "Number of orders to create per market (default: 1).")
	rootCmd.AddCommand(txExchangeBatchOrdersCmd)

	txWasmStoreCodeCmd := &cobra.Command{
		Use:   "tx-wasm-store-code",
		Short: "Run stresstest with x/wasm.MsgStoreCode transactions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if verboseOutput {
				log.DefaultLogger.SetLevel(log.DebugLevel)
			}

			orPanic(readAccounts(&stressCfg, accountFile, numOfAccounts))

			wasmStoreCodeProvider, err := payload.NewWasmStoreCodeProvider(stressCfg.MinGasPrice)
			if err != nil {
				return errors.Wrap(err, "failed to initiate wasm code store stress provider")
			}

			if err := stresser.Stress(rootCtx, stressCfg, wasmStoreCodeProvider); err != nil {
				log.Errorf("❌ benchmark failed:\n\n%s", err)
				os.Exit(-1)
			}

			return nil
		},
	}
	rootCmd.AddCommand(txWasmStoreCodeCmd)

	txWasmInitContractCmd := &cobra.Command{
		Use:   "tx-wasm-init-contract",
		Short: "Run stresstest with x/wasm.MsgInstantiateContract transactions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if verboseOutput {
				log.DefaultLogger.SetLevel(log.DebugLevel)
			}

			orPanic(readAccounts(&stressCfg, accountFile, numOfAccounts))

			queryClient := chain.NewClient(
				stressCfg.ChainID,
				stressCfg.NodeAddress,
				stressCfg.GRPCAddress,
			)

			wasmInitContractProvider, err := payload.NewWasmInitContractProvider(
				queryClient,
				stressCfg.MinGasPrice,
			)
			if err != nil {
				return errors.Wrap(err, "failed to initiate wasm init contract stress provider")
			}

			if err := stresser.Stress(rootCtx, stressCfg, wasmInitContractProvider); err != nil {
				log.Errorf("❌ benchmark failed:\n\n%s", err)
				os.Exit(-1)
			}

			return nil
		},
	}
	rootCmd.AddCommand(txWasmInitContractCmd)

	txWasmExecContractCmd := &cobra.Command{
		Use:   "tx-wasm-exec-contract",
		Short: "Run stresstest with x/wasm.MsgExecuteContract transactions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if verboseOutput {
				log.DefaultLogger.SetLevel(log.DebugLevel)
			}

			orPanic(readAccounts(&stressCfg, accountFile, numOfAccounts))

			queryClient := chain.NewClient(
				stressCfg.ChainID,
				stressCfg.NodeAddress,
				stressCfg.GRPCAddress,
			)

			wasmExecContractProvider, err := payload.NewWasmExecContractProvider(
				queryClient,
				stressCfg.MinGasPrice,
			)
			if err != nil {
				return errors.Wrap(err, "failed to initiate wasm exec stress provider")
			}

			if err := stresser.Stress(rootCtx, stressCfg, wasmExecContractProvider); err != nil {
				log.Errorf("❌ benchmark failed:\n\n%s", err)
				os.Exit(-1)
			}

			return nil
		},
	}
	rootCmd.AddCommand(txWasmExecContractCmd)

	var mixedPayloadConfigPath string

	txMixedPayloadCmd := &cobra.Command{
		Use:   "tx-mixed-payload",
		Short: "Run stresstest with mixed payload types configured via YAML.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if verboseOutput {
				log.DefaultLogger.SetLevel(log.DebugLevel)
			}

			if mixedPayloadConfigPath == "" {
				return errors.New("--config is required")
			}

			mixedCfg, err := payload.LoadMixedPayloadConfig(mixedPayloadConfigPath)
			if err != nil {
				return errors.Wrap(err, "failed to load mixed payload config")
			}

			if err := applyStresserConfigFromYAML(&stressCfg, mixedCfg.StresserConfig, cmd); err != nil {
				return errors.Wrap(err, "failed to apply stresser config from YAML")
			}

			orPanic(readAccounts(&stressCfg, accountFile, numOfAccounts))

			queryClient := chain.NewClient(
				stressCfg.ChainID,
				stressCfg.NodeAddress,
				stressCfg.GRPCAddress,
			)

			mixedProvider, err := payload.NewMixedPayloadProvider(
				mixedCfg,
				stressCfg.MinGasPrice,
				stressCfg.EthChainID,
				queryClient,
			)
			if err != nil {
				return errors.Wrap(err, "failed to create mixed payload provider")
			}

			if err := stresser.Stress(rootCtx, stressCfg, mixedProvider); err != nil {
				log.Errorf("❌ benchmark failed:\n\n%s", err)
				os.Exit(-1)
			}

			return nil
		},
	}
	txMixedPayloadCmd.Flags().StringVar(&mixedPayloadConfigPath, "config", "", "Path to YAML config file for mixed payload (required).")
	txMixedPayloadCmd.MarkFlagRequired("config")
	rootCmd.AddCommand(txMixedPayloadCmd)

	var replayCfg replay.TxReplayConfig

	txnsReplayCmd := &cobra.Command{
		Use:   "tx-replay",
		Short: "Run stresstest with state replay transactions.",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if replayCfg.CometRPC == "" {
				return errors.New("--sniffer-rpc is required for remote sniffing.")
			}
			if replayCfg.StartHeight == 0 {
				return errors.New("--sniffer-start-height is required for remote sniffing.")
			}

			if replayCfg.EndHeight > 0 && replayCfg.StartHeight >= replayCfg.EndHeight {
				return errors.New("--sniffer-start-height must be less than --sniffer-end-height")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if verboseOutput {
				log.DefaultLogger.SetLevel(log.DebugLevel)
			}

			queryClient := chain.NewClient(
				stressCfg.ChainID,
				stressCfg.NodeAddress,
				stressCfg.GRPCAddress,
			)
			var txnsReplayProvider payload.TxProvider

			sniffer, err := replay.NewSniffer(
				&replay.TxReplayConfig{
					CometRPC:    replayCfg.CometRPC,
					StartHeight: int64(replayCfg.StartHeight),
					EndHeight:   replayCfg.EndHeight,
				},
				rootCtx,
			)
			if err != nil {
				return errors.Wrap(err, "failed to initiate txns sniffer")
			}
			defer sniffer.Close()
			go func() {
				sniffer.Start()
			}()

			txnsReplayProvider, err = payload.NewTxnsReplayStressProvider(
				queryClient,
				sniffer.Blocks(),
				sniffer.Errors(),
				sniffer.Done(),
			)
			if err != nil {
				return errors.Wrap(err, "failed to initiate txns replay stress provider")
			}

			// Explicitly set to false to avoid waiting for block confirmation, so we can replay faster (otherwise its 1TX per block)
			stressCfg.AwaitTxConfirmation = false

			if err := stresser.StressReplay(rootCtx, stressCfg, txnsReplayProvider); err != nil {
				log.Errorf("❌ benchmark failed:\n\n%s", err)
				os.Exit(-1)
			}

			return nil
		},
	}
	txnsReplayCmd.Flags().StringVar(&replayCfg.CometRPC, "sniffer-rpc", "http://127.0.0.1:26657", "RPC endpoint to use for the txns sniffer.")
	txnsReplayCmd.Flags().Int64Var(&replayCfg.StartHeight, "sniffer-start-height", 0, "Start height for the txns sniffer (must be devnetified height + 1).")
	txnsReplayCmd.Flags().Int64Var(&replayCfg.EndHeight, "sniffer-end-height", 0, "End height for the txns sniffer (optional, defaults to endless mode).")

	// Gas limit fuzzing configuration flags
	txnsReplayCmd.Flags().BoolVar(&stressCfg.GasFuzzing.Enabled, "gas-fuzz", false, "Enable gas limit fuzzing for transactions during replay.")
	txnsReplayCmd.Flags().StringVar(&stressCfg.GasFuzzing.Strategy, "gas-fuzz-strategy", "random", "Gas limit fuzzing strategy: random, boundary, incremental, chaos.")
	txnsReplayCmd.Flags().IntVar(&stressCfg.GasFuzzing.FuzzPercentage, "gas-fuzz-percentage", 100, "Percentage of transactions to fuzz (0-100).")
	txnsReplayCmd.Flags().Float64Var(&stressCfg.GasFuzzing.GasLimitMultiplierMin, "gas-limit-min", 0.01, "Minimum gas limit multiplier.")
	txnsReplayCmd.Flags().Float64Var(&stressCfg.GasFuzzing.GasLimitMultiplierMax, "gas-limit-max", 1.0, "Maximum gas limit multiplier.")
	txnsReplayCmd.Flags().Int64Var(&stressCfg.GasFuzzing.Seed, "gas-fuzz-seed", 0, "Seed for deterministic fuzzing (0 for random).")
	txnsReplayCmd.Flags().BoolVar(&stressCfg.GasFuzzing.VerboseLogging, "gas-fuzz-verbose", false, "Enable detailed before/after transaction logging for gas fuzzing.")

	rootCmd.AddCommand(txnsReplayCmd)

	// Deploy ERC20 contracts command
	var (
		deployRPCURL       string
		deployStakerKey    string
		deployGasLimit     uint64
		deployGasPrice     *big.Int
		deployAccountsFile string
		deployGasPriceStr  string
	)

	var (
		deployMintAmount  string
		deployMintEnabled bool
	)

	deployCmd := &cobra.Command{
		Use:   "deploy-erc20",
		Short: "部署 ERC20、EntryPoint 和 Factory 合约",
		Long: `部署 ERC20、EntryPoint 和 Factory 合约到链上。

如果没有提供私钥，将从 chain-stresser-deploy/instances/0/accounts.json 读取第一个私钥。
部署成功后，可以选择为 Light Account 铸造代币。`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// 解析 gas price
			gasPrice := deployGasPrice
			if deployGasPriceStr != "" {
				var ok bool
				gasPrice, ok = new(big.Int).SetString(deployGasPriceStr, 10)
				if !ok {
					return errors.New("无效的 gas price")
				}
			}

			// 构建部署配置
			cfg := deploy.Config{
				RPCURL:       deployRPCURL,
				StakerKey:    deployStakerKey,
				GasLimit:     deployGasLimit,
				GasPrice:     gasPrice,
				AccountsFile: deployAccountsFile,
			}

			// 执行部署
			result, err := deploy.DeployERC20Contracts(cfg)
			if err != nil {
				return err
			}

			// 保存到 build 目录
			projectRoot, err := getProjectRoot()
			if err != nil {
				log.Errorf("获取项目根目录失败: %v", err)
			} else {
				buildDir := filepath.Join(projectRoot, "build")
				envFile := filepath.Join(buildDir, "erc20_contracts.env")
				if err := deploy.SaveToFile(result, envFile); err != nil {
					log.Errorf("保存文件失败: %v", err)
				}
			}

			// 如果启用了铸造，执行铸造操作
			if deployMintEnabled {
				log.Info("开始为 Light Account 铸造代币...")

				// 解析铸造数量（默认 100000 * 10^18）
				mintAmount := big.NewInt(0)
				if deployMintAmount != "" {
					var ok bool
					mintAmount, ok = mintAmount.SetString(deployMintAmount, 10)
					if !ok {
						return errors.New("无效的铸造数量")
					}
				} else {
					// 默认值: 100000 * 10^18
					mintAmount = new(big.Int).Mul(big.NewInt(100000), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
				}

				mintCfg := deploy.MintConfig{
					RPCURL:        deployRPCURL,
					TokenAddr:     result.TokenAddr,
					FactoryAddr:   result.FactoryAddr,
					StakerKey:     deployStakerKey,
					AccountsFile:  deployAccountsFile,
					AmountPerAcct: mintAmount,
					GasLimit:      deployGasLimit,
					GasPrice:      gasPrice,
				}

				if err := deploy.MintToLightAccounts(mintCfg); err != nil {
					return errors.Wrap(err, "铸造代币失败")
				}
			}

			return nil
		},
	}

	deployCmd.Flags().StringVar(&deployRPCURL, "rpc-url", "http://127.0.0.1:8545", "RPC 端点地址")
	deployCmd.Flags().StringVar(&deployStakerKey, "staker-key", "", "部署者私钥（hex 格式）。如果不提供，将从 accounts.json 读取")
	deployCmd.Flags().Uint64Var(&deployGasLimit, "gas-limit", 70000000, "Gas limit (默认: 70000000，链最大限制: 75000000)")
	deployCmd.Flags().StringVar(&deployAccountsFile, "accounts-file", "", "accounts.json 文件路径（默认: chain-stresser-deploy/instances/0/accounts.json）")

	// 默认 gas price: 3000000 wei
	deployGasPrice = big.NewInt(3000000)
	deployCmd.Flags().StringVar(&deployGasPriceStr, "gas-price", "3000000", "Gas price (wei)")

	// 铸造相关参数
	deployCmd.Flags().BoolVar(&deployMintEnabled, "mint", false, "部署后为 Light Account 铸造代币")
	deployCmd.Flags().StringVar(&deployMintAmount, "mint-amount", "", "每个账户的铸造数量（wei，默认: 100000000000000000000000，即 100000 tokens）")

	rootCmd.AddCommand(deployCmd)

	orPanic(rootCmd.Execute())
}

const bannerStr = `
┏┓┓   •    ┏┓       
┃ ┣┓┏┓┓┏┓  ┗┓╋┏┓┏┓┏┏
┗┛┛┗┗┻┗┛┗  ┗┛┗┛ ┗ ┛┛

Ultimate benchmarking tool for Biya Chain 🔥
`

func readAccounts(
	cfg *stresser.StressConfig,
	accountFile string,
	numOfAccounts int,
) error {
	if numOfAccounts <= 0 {
		return errors.New("number of accounts must be greater than 0")
	}

	keysRaw, err := os.ReadFile(accountFile)
	if err != nil {
		return errors.Wrap(err, "reading account file failed")
	} else if err := json.Unmarshal(keysRaw, &cfg.Accounts); err != nil {
		return errors.Wrap(err, "parsing account file failed")
	} else if numOfAccounts > len(cfg.Accounts) {
		return errors.New("number of accounts is greater than the number of provided private keys")
	}

	cfg.Accounts = cfg.Accounts[:numOfAccounts]
	return nil
}

func orPanic(err error) {
	if err != nil {
		panic(err)
	}
}

func strOrPanic(out string, err error) string {
	if err != nil {
		panic(err)
	}

	return out
}

// queryERC20Balance 查询指定地址的 ERC20 代币余额
func queryERC20Balance(client *ethclient.Client, tokenAddr, accountAddr string) (*big.Int, error) {
	// ERC20 balanceOf ABI
	erc20ABIJSON := `[{"constant":true,"inputs":[{"name":"account","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`

	contractABI, err := abi.JSON(strings.NewReader(erc20ABIJSON))
	if err != nil {
		return nil, errors.Wrap(err, "解析 ERC20 ABI 失败")
	}

	// 打包 balanceOf 调用
	callData, err := contractABI.Pack("balanceOf", ethcmn.HexToAddress(accountAddr))
	if err != nil {
		return nil, errors.Wrap(err, "打包 balanceOf 调用失败")
	}

	// 调用合约
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &[]ethcmn.Address{ethcmn.HexToAddress(tokenAddr)}[0],
		Data: callData,
	}, nil)
	if err != nil {
		return nil, errors.Wrap(err, "调用合约失败")
	}

	// 解包结果
	var balance *big.Int
	err = contractABI.UnpackIntoInterface(&balance, "balanceOf", result)
	if err != nil {
		return nil, errors.Wrap(err, "解包结果失败")
	}

	return balance, nil
}

// formatTokenAmount 格式化代币数量（从 wei 转换为 tokens）
func formatTokenAmount(amount *big.Int) string {
	if amount == nil {
		return "0"
	}

	// 将 wei 转换为 tokens (除以 10^18)
	divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	tokens := new(big.Float).SetInt(amount)
	tokens.Quo(tokens, new(big.Float).SetInt(divisor))

	return tokens.Text('f', 2)
}

func applyStresserConfigFromYAML(
	stressCfg *stresser.StressConfig,
	yamlCfg *payload.StresserConfig,
	cmd *cobra.Command,
) error {
	if yamlCfg == nil {
		return nil
	}

	if yamlCfg.ChainID != "" && !cmd.Flags().Changed("chain-id") {
		stressCfg.ChainID = yamlCfg.ChainID
	}

	if yamlCfg.EthChainID != 0 && !cmd.Flags().Changed("eth-chain-id") {
		stressCfg.EthChainID = yamlCfg.EthChainID
	}

	if yamlCfg.MinGasPrice != "" && !cmd.Flags().Changed("min-gas-price") {
		stressCfg.MinGasPrice = yamlCfg.MinGasPrice
	}

	if yamlCfg.NodeAddress != "" && !cmd.Flags().Changed("node-addr") {
		stressCfg.NodeAddress = yamlCfg.NodeAddress
	}

	if yamlCfg.GRPCAddress != "" && !cmd.Flags().Changed("grpc-addr") {
		stressCfg.GRPCAddress = yamlCfg.GRPCAddress
	}

	if yamlCfg.AwaitTxConfirmation != nil && !cmd.Flags().Changed("await") {
		stressCfg.AwaitTxConfirmation = *yamlCfg.AwaitTxConfirmation
	}

	if yamlCfg.NumOfTransactions != 0 && !cmd.Flags().Changed("transactions") {
		stressCfg.NumOfTransactions = yamlCfg.NumOfTransactions
	}

	if yamlCfg.RateTPS != 0 && !cmd.Flags().Changed("rate-tps") {
		stressCfg.RateLimit.TxPerSecond = yamlCfg.RateTPS
	}

	if yamlCfg.RateBytes != 0 && !cmd.Flags().Changed("rate-bytes") {
		stressCfg.RateLimit.BytesPerSecond = yamlCfg.RateBytes
	}

	if yamlCfg.RateGas != 0 && !cmd.Flags().Changed("rate-gas") {
		stressCfg.RateLimit.GasPerSecond = yamlCfg.RateGas
	}

	if yamlCfg.RateBurstSize != 0 && !cmd.Flags().Changed("rate-burst-size") {
		stressCfg.RateLimit.Burst.Size = yamlCfg.RateBurstSize
	}

	return nil
}

func getProjectRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// 查找 go.mod 文件
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return wd, nil
}
