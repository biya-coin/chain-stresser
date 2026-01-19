package deploy

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/pkg/errors"
	log "github.com/xlab/suplog"

	"github.com/biya-coin/chain-stresser/v2/chain"
	contract "github.com/biya-coin/chain-stresser/v2/eth/solidity/LightAccountFactory"
)

// MintConfig 铸造配置
type MintConfig struct {
	RPCURL        string
	TokenAddr     common.Address
	FactoryAddr   common.Address
	StakerKey     string
	AccountsFile  string
	AmountPerAcct *big.Int
	GasLimit      uint64
	GasPrice      *big.Int
}

// MintToLightAccounts 为 Light Account 铸造代币
func MintToLightAccounts(cfg MintConfig) error {
	// 连接 RPC
	client, err := ethclient.Dial(cfg.RPCURL)
	if err != nil {
		return errors.Wrapf(err, "连接 RPC 失败: %s", cfg.RPCURL)
	}
	defer client.Close()

	// 获取链 ID
	chainID, err := client.ChainID(context.Background())
	if err != nil {
		return errors.Wrap(err, "获取链 ID 失败")
	}

	// 解析私钥
	var privateKey *ecdsa.PrivateKey
	if cfg.StakerKey != "" {
		privateKey, err = crypto.HexToECDSA(cfg.StakerKey)
		if err != nil {
			return errors.Wrap(err, "解析私钥失败")
		}
	} else {
		// 从 accounts.json 读取第一个私钥
		stakerKey, err := getStakerKey("", cfg.AccountsFile)
		if err != nil {
			return errors.Wrap(err, "获取私钥失败")
		}
		privateKey, err = crypto.HexToECDSA(stakerKey)
		if err != nil {
			return errors.Wrap(err, "解析私钥失败")
		}
	}

	// 创建交易授权
	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, chainID)
	if err != nil {
		return errors.Wrap(err, "创建交易授权失败")
	}
	if cfg.GasLimit > 0 {
		auth.GasLimit = cfg.GasLimit
	}
	if cfg.GasPrice != nil {
		auth.GasPrice = cfg.GasPrice
	}

	// 读取 accounts.json
	accountsFile := cfg.AccountsFile
	if accountsFile == "" {
		projectRoot, err := getProjectRoot()
		if err != nil {
			return errors.Wrap(err, "获取项目根目录失败")
		}
		accountsFile = filepath.Join(projectRoot, "chain-stresser-deploy", "instances", "0", "accounts.json")
	}

	data, err := os.ReadFile(accountsFile)
	if err != nil {
		return errors.Wrapf(err, "读取文件失败: %s", accountsFile)
	}

	var accounts []chain.Secp256k1PrivateKey
	if err := json.Unmarshal(data, &accounts); err != nil {
		return errors.Wrap(err, "解析 accounts.json 失败")
	}

	if len(accounts) == 0 {
		return errors.New("accounts.json 中没有账户")
	}

	log.Infof("找到 %d 个账户，开始计算 Light Account 地址并铸造代币...", len(accounts))

	// 创建 Factory 合约实例
	factoryContract, err := contract.NewLightAccountFactory(cfg.FactoryAddr, client)
	if err != nil {
		return errors.Wrap(err, "创建 Factory 合约实例失败")
	}

	// 获取部署者地址
	deployerAddr := crypto.PubkeyToAddress(privateKey.PublicKey)
	log.Infof("部署者地址: %s", deployerAddr.Hex())

	// 准备 gas 参数
	gasLimit := cfg.GasLimit
	if gasLimit == 0 {
		gasLimit = 100000 // 默认 gas limit
	}
	gasPrice := cfg.GasPrice
	if gasPrice == nil {
		gasPrice = big.NewInt(3000000)
	}

	// 并行铸造配置
	parallelWorkers := 10 // 并行数量
	totalAccounts := len(accounts)

	// 使用 waitgroup 管理并发
	type mintTask struct {
		index            int
		accountKey       chain.Secp256k1PrivateKey
		eoaAddr          common.Address
		lightAccountAddr common.Address
	}

	var mu sync.Mutex
	successCount := 0
	failedCount := 0

	// 第一阶段：并行计算所有 Light Account 地址
	log.Info("阶段 1: 计算所有 Light Account 地址...")
	addrWg := sync.WaitGroup{}
	addrResults := make([]mintTask, totalAccounts)
	addrMutex := sync.Mutex{}

	for i := 0; i < parallelWorkers; i++ {
		addrWg.Add(1)
		go func(workerID int) {
			defer addrWg.Done()
			for j := workerID; j < totalAccounts; j += parallelWorkers {
				accountKey := accounts[j]
				accountPrivateKey, err := crypto.ToECDSA(accountKey)
				if err != nil {
					log.Errorf("账户 %d: 解析私钥失败: %v", j+1, err)
					continue
				}

				eoaAddr := crypto.PubkeyToAddress(accountPrivateKey.PublicKey)
				salt := big.NewInt(1) // 使用 salt=1 与压测保持一致
				lightAccountAddr, err := factoryContract.GetAddress(&bind.CallOpts{
					Context: context.Background(),
				}, eoaAddr, salt)
				if err != nil {
					log.Errorf("账户 %d: 获取 Light Account 地址失败: %v", j+1, err)
					continue
				}

				addrMutex.Lock()
				addrResults[j] = mintTask{
					index:            j,
					accountKey:       accountKey,
					eoaAddr:          eoaAddr,
					lightAccountAddr: lightAccountAddr,
				}
				addrMutex.Unlock()

				if (j+1)%50 == 0 || j == totalAccounts-1 {
					log.Infof("  已计算地址: %d / %d", j+1, totalAccounts)
				}
			}
		}(i)
	}
	addrWg.Wait()

	// 过滤掉失败的地址计算
	validTasks := make([]mintTask, 0, totalAccounts)
	for _, task := range addrResults {
		if task.lightAccountAddr != (common.Address{}) {
			validTasks = append(validTasks, task)
		}
	}

	log.Infof("✓ 地址计算完成: %d / %d", len(validTasks), totalAccounts)
	log.Infof("阶段 2: 开始批量铸造代币（批次串行发送以确保 nonce 连续）...")

	// 创建 ERC20 合约实例（使用 ABI 直接调用）
	erc20ABI := `[{"inputs":[{"internalType":"address[]","name":"recipients","type":"address[]"},{"internalType":"uint256","name":"amount","type":"uint256"}],"name":"batchMint","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"to","type":"address"},{"internalType":"uint256","name":"amount","type":"uint256"}],"name":"mint","outputs":[],"stateMutability":"nonpayable","type":"function"}]`

	parsedABI, err := abi.JSON(strings.NewReader(erc20ABI))
	if err != nil {
		return errors.Wrap(err, "解析 ERC20 ABI 失败")
	}

	// 第二阶段：批量铸造（每批多个地址，批次之间串行发送）
	// 批量大小：每批 5 个地址（参考 Python 脚本，避免 gas limit 过高）
	batchSize := 5
	totalBatches := (len(validTasks) + batchSize - 1) / batchSize

	log.Infof("总账户数: %d, 批次数: %d, 每批: %d 个地址", len(validTasks), totalBatches, batchSize)

	// Nonce 管理：使用互斥锁确保 nonce 连续
	nonceMutex := sync.Mutex{}
	currentNonce := uint64(0)
	getNextNonce := func() (uint64, error) {
		nonceMutex.Lock()
		defer nonceMutex.Unlock()

		if currentNonce == 0 {
			// 首次获取，从链上查询
			nonce, err := client.PendingNonceAt(context.Background(), deployerAddr)
			if err != nil {
				return 0, err
			}
			currentNonce = nonce
		}
		nonce := currentNonce
		currentNonce++
		return nonce, nil
	}

	updateNonceFromError := func(errMsg string) bool {
		// 解析错误信息：invalid nonce; got 74, expected 75: invalid sequence
		// 或者：invalid sequence; got 74, expected 75
		nonceMutex.Lock()
		defer nonceMutex.Unlock()

		// 尝试提取期望的 nonce
		expectedNonceStr := ""
		if strings.Contains(errMsg, "invalid nonce") {
			// 格式：invalid nonce; got 74, expected 75
			parts := strings.Split(errMsg, "expected ")
			if len(parts) > 1 {
				expectedNonceStr = strings.TrimSpace(strings.Split(parts[1], ":")[0])
			}
		} else if strings.Contains(errMsg, "invalid sequence") {
			// 格式：invalid sequence; got 74, expected 75
			parts := strings.Split(errMsg, "expected ")
			if len(parts) > 1 {
				expectedNonceStr = strings.TrimSpace(strings.Split(parts[1], ":")[0])
			}
		}

		if expectedNonceStr != "" {
			expectedNonce, parseErr := strconv.ParseUint(expectedNonceStr, 10, 64)
			if parseErr == nil {
				// 更新当前 nonce 为期望的值
				currentNonce = expectedNonce
				return true
			}
		}
		return false
	}

	// 串行处理每个批次（确保 nonce 连续）
	for batchIdx := 0; batchIdx < len(validTasks); batchIdx += batchSize {
		end := batchIdx + batchSize
		if end > len(validTasks) {
			end = len(validTasks)
		}

		batchNum := batchIdx/batchSize + 1
		batch := validTasks[batchIdx:end]

		// 构建地址数组
		recipients := make([]common.Address, len(batch))
		for i, task := range batch {
			recipients[i] = task.lightAccountAddr
		}

		log.Infof("批次 %d/%d: 铸造 %d 个地址...", batchNum, totalBatches, len(batch))

		// 打包 batchMint 调用数据
		callData, err := parsedABI.Pack("batchMint", recipients, cfg.AmountPerAcct)
		if err != nil {
			log.Errorf("批次 %d: 打包调用数据失败: %v", batchNum, err)
			mu.Lock()
			failedCount += len(batch)
			mu.Unlock()
			continue
		}

		// 估算 gas
		estimatedGas, err := client.EstimateGas(context.Background(), ethereum.CallMsg{
			From:  deployerAddr,
			To:    &cfg.TokenAddr,
			Data:  callData,
			Value: big.NewInt(0),
		})
		if err != nil {
			log.Errorf("批次 %d: Gas 估算失败: %v，使用默认值", batchNum, err)
			estimatedGas = 200000 + uint64(len(batch)*30000) // 基础 gas + 每个地址的 gas
		}

		// 确保 gas limit 不超过链的限制
		if estimatedGas > cfg.GasLimit {
			estimatedGas = cfg.GasLimit
		}

		// 发送交易（带重试机制处理 nonce 错误）
		maxRetries := 3
		sent := false
		for retry := 0; retry < maxRetries; retry++ {
			// 获取下一个 nonce（受互斥锁保护）
			nonce, err := getNextNonce()
			if err != nil {
				log.Errorf("批次 %d: 获取 nonce 失败: %v", batchNum, err)
				break
			}

			// 构建交易
			tx := types.NewTransaction(
				nonce,
				cfg.TokenAddr,
				big.NewInt(0),
				estimatedGas,
				gasPrice,
				callData,
			)

			// 签名交易
			signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), privateKey)
			if err != nil {
				log.Errorf("批次 %d: 签名交易失败: %v", batchNum, err)
				break
			}

			// 异步发送交易（不等待确认）
			txHash := signedTx.Hash()
			err = client.SendTransaction(context.Background(), signedTx)
			if err != nil {
				errMsg := err.Error()
				// 检查是否是 nonce/sequence 错误
				if strings.Contains(errMsg, "invalid nonce") || strings.Contains(errMsg, "invalid sequence") {
					// 尝试从错误中提取期望的 nonce 并更新
					if updateNonceFromError(errMsg) {
						log.Warningf("批次 %d: nonce 错误，已更新 nonce 并重试 (重试 %d/%d)", batchNum, retry+1, maxRetries)
						continue // 重试
					} else {
						// 无法解析错误，重新查询 nonce
						nonceMutex.Lock()
						newNonce, queryErr := client.PendingNonceAt(context.Background(), deployerAddr)
						if queryErr == nil {
							currentNonce = newNonce
						}
						nonceMutex.Unlock()
						if retry < maxRetries-1 {
							log.Warningf("批次 %d: nonce 错误，重新查询 nonce 并重试 (重试 %d/%d)", batchNum, retry+1, maxRetries)
							continue // 重试
						}
					}
				}
				// 其他错误或重试次数用完
				log.Errorf("批次 %d: 发送交易失败: %v", batchNum, err)
				break
			}

			// 发送成功
			mu.Lock()
			successCount += len(batch)
			log.Infof("批次 %d: ✓ 已发送 (tx: %s, nonce: %d)", batchNum, txHash.Hex()[:10]+"...", nonce)
			mu.Unlock()
			sent = true
			break
		}

		if !sent {
			mu.Lock()
			failedCount += len(batch)
			mu.Unlock()
		}

		// 每完成 5 个批次或最后一个批次时显示进度
		if batchNum%5 == 0 || batchNum == totalBatches {
			mu.Lock()
			currentSuccess := successCount
			currentFailed := failedCount
			mu.Unlock()
			log.Infof("批次进度: %d / %d (成功: %d, 失败: %d)", batchNum, totalBatches, currentSuccess, currentFailed)
		}
	}

	log.Infof("✓ 铸造完成: 成功 %d / 失败 %d / 总计 %d", successCount, failedCount, len(validTasks))
	return nil
}
