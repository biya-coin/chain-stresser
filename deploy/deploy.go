package deploy

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/pkg/errors"
	log "github.com/xlab/suplog"

	"github.com/biya-coin/chain-stresser/v2/chain"
)

// Config 部署配置
type Config struct {
	RPCURL       string
	StakerKey    string
	GasLimit     uint64
	GasPrice     *big.Int
	AccountsFile string
}

// DeployResult 部署结果
type DeployResult struct {
	TokenAddr      common.Address
	EntryPointAddr common.Address
	FactoryAddr    common.Address
}

// DeployERC20Contracts 部署 ERC20、EntryPoint 和 Factory 合约
func DeployERC20Contracts(cfg Config) (*DeployResult, error) {
	// 设置默认 gas limit（如果未提供）
	gasLimit := cfg.GasLimit
	if gasLimit == 0 {
		gasLimit = 70000000 // 默认值，略小于链的最大限制 75000000
	}

	gasPrice := cfg.GasPrice
	if gasPrice == nil {
		gasPrice = big.NewInt(3000000) // 默认 gas price: 3000000 wei
	}

	// 获取私钥
	stakerKey, err := getStakerKey(cfg.StakerKey, cfg.AccountsFile)
	if err != nil {
		return nil, errors.Wrap(err, "获取私钥失败")
	}

	// 连接 RPC
	client, err := ethclient.Dial(cfg.RPCURL)
	if err != nil {
		return nil, errors.Wrapf(err, "连接 RPC 失败: %s", cfg.RPCURL)
	}
	defer client.Close()

	// 获取链 ID
	chainID, err := client.ChainID(context.Background())
	if err != nil {
		return nil, errors.Wrap(err, "获取链 ID 失败")
	}

	// 创建交易授权
	privateKey, err := crypto.HexToECDSA(stakerKey)
	if err != nil {
		return nil, errors.Wrap(err, "解析私钥失败")
	}

	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, chainID)
	if err != nil {
		return nil, errors.Wrap(err, "创建交易授权失败")
	}
	auth.GasLimit = gasLimit
	auth.GasPrice = gasPrice

	// 获取账户地址
	accountAddress := crypto.PubkeyToAddress(privateKey.PublicKey)
	log.Info("部署账户地址: ", accountAddress.Hex())

	// 1. 部署 ERC20
	log.Info("部署 ERC20 合约...")
	tokenAddr, err := deployERC20(client, auth, cfg.RPCURL, gasLimit, gasPrice, privateKey)
	if err != nil {
		return nil, errors.Wrap(err, "部署 ERC20 失败")
	}
	fmt.Println(tokenAddr.Hex())

	// 2. 部署 EntryPoint
	log.Info("部署 EntryPoint 合约...")
	entryPointAddr, err := deployEntryPoint(client, auth)
	if err != nil {
		return nil, errors.Wrap(err, "部署 EntryPoint 失败")
	}
	fmt.Println(entryPointAddr.Hex())

	// 3. 部署 Factory
	log.Info("部署 Factory 合约...")
	factoryAddr, err := deployFactory(client, auth, accountAddress, entryPointAddr, cfg.RPCURL, gasLimit, gasPrice, privateKey)
	if err != nil {
		return nil, errors.Wrap(err, "部署 Factory 失败")
	}
	fmt.Println(factoryAddr.Hex())

	return &DeployResult{
		TokenAddr:      tokenAddr,
		EntryPointAddr: entryPointAddr,
		FactoryAddr:    factoryAddr,
	}, nil
}

// SaveToFile 保存部署结果到文件
func SaveToFile(result *DeployResult, outputFile string) error {
	envContent := fmt.Sprintf("TOKEN_ADDR=%s\nENTRYPOINT_ADDR=%s\nFACTORY_ADDR=%s\n",
		result.TokenAddr.Hex(), result.EntryPointAddr.Hex(), result.FactoryAddr.Hex())

	// 确保目录存在
	dir := filepath.Dir(outputFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return errors.Wrapf(err, "创建目录失败: %s", dir)
	}

	if err := os.WriteFile(outputFile, []byte(envContent), 0644); err != nil {
		return errors.Wrapf(err, "保存文件失败: %s", outputFile)
	}

	log.Info("合约地址已保存到: ", outputFile)
	return nil
}

// LoadFromFile 从文件加载部署结果
func LoadFromFile(filePath string) (*DeployResult, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, errors.Wrapf(err, "读取文件失败: %s", filePath)
	}

	result := &DeployResult{}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		switch key {
		case "TOKEN_ADDR":
			result.TokenAddr = common.HexToAddress(value)
		case "ENTRYPOINT_ADDR":
			result.EntryPointAddr = common.HexToAddress(value)
		case "FACTORY_ADDR":
			result.FactoryAddr = common.HexToAddress(value)
		}
	}

	if result.TokenAddr == (common.Address{}) || result.FactoryAddr == (common.Address{}) {
		return nil, errors.New("合约地址不完整")
	}

	return result, nil
}

func getStakerKey(stakerKey, accountsFile string) (string, error) {
	if stakerKey != "" {
		// 移除 0x 前缀
		key := strings.TrimPrefix(stakerKey, "0x")
		if len(key) != 64 {
			return "", errors.New("私钥必须是 64 个十六进制字符")
		}
		return key, nil
	}

	// 从 accounts.json 读取
	if accountsFile == "" {
		// 默认路径
		projectRoot, err := getProjectRoot()
		if err != nil {
			return "", errors.Wrap(err, "获取项目根目录失败")
		}
		accountsFile = filepath.Join(projectRoot, "chain-stresser-deploy", "instances", "0", "accounts.json")
	}

	if _, err := os.Stat(accountsFile); os.IsNotExist(err) {
		return "", errors.Errorf("找不到 accounts.json 文件: %s", accountsFile)
	}

	// 读取 accounts.json
	data, err := os.ReadFile(accountsFile)
	if err != nil {
		return "", errors.Wrapf(err, "读取文件失败: %s", accountsFile)
	}

	var accounts []chain.Secp256k1PrivateKey
	if err := json.Unmarshal(data, &accounts); err != nil {
		return "", errors.Wrap(err, "解析 accounts.json 失败")
	}

	if len(accounts) == 0 {
		return "", errors.New("accounts.json 中没有账户")
	}

	// 返回第一个私钥的 hex 格式
	firstKey := accounts[0]
	return hex.EncodeToString(firstKey), nil
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

func deployERC20(client *ethclient.Client, auth *bind.TransactOpts, rpcURL string, gasLimit uint64, gasPrice *big.Int, privateKey *ecdsa.PrivateKey) (common.Address, error) {
	projectRoot, err := getProjectRoot()
	if err != nil {
		return common.Address{}, err
	}

	erc20File := filepath.Join(projectRoot, "eth", "solidity", "ERC20.sol")

	// 检查文件是否存在
	if _, err := os.Stat(erc20File); os.IsNotExist(err) {
		return common.Address{}, errors.Errorf("找不到 ERC20.sol 文件: %s", erc20File)
	}

	// 使用 etherman 部署
	cmd := exec.Command("etherman",
		"-N", "ERC20",
		"-S", erc20File,
		"-P", hex.EncodeToString(crypto.FromECDSA(privateKey)),
		"-E", rpcURL,
		"-G", fmt.Sprintf("%d", gasLimit),
		"-L", gasPrice.String(),
		"deploy", "Test Token", "TEST", "18",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return common.Address{}, errors.Wrapf(err, "etherman 执行失败: %s", string(output))
	}

	// 从输出中提取地址
	outputStr := string(output)
	lines := strings.Split(outputStr, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "0x") && len(line) >= 42 {
			addr := common.HexToAddress(strings.TrimSpace(line))
			if addr != (common.Address{}) {
				return addr, nil
			}
		}
	}

	// 尝试从输出中提取地址（更宽松的匹配）
	for _, line := range lines {
		if idx := strings.Index(line, "0x"); idx >= 0 {
			addrStr := line[idx:]
			if len(addrStr) >= 42 {
				addrStr = addrStr[:42]
				addr := common.HexToAddress(addrStr)
				if addr != (common.Address{}) {
					return addr, nil
				}
			}
		}
	}

	return common.Address{}, errors.Errorf("无法从输出中提取合约地址: %s", outputStr)
}

func deployEntryPoint(client *ethclient.Client, auth *bind.TransactOpts) (common.Address, error) {
	projectRoot, err := getProjectRoot()
	if err != nil {
		return common.Address{}, err
	}

	// EntryPoint 需要先编译
	entryPointDir := filepath.Join(projectRoot, "eth", "solidity", "account-abstraction")

	log.Info("编译 EntryPoint...")

	// OpenZeppelin 合约存放在 eth/solidity/openzeppelin 目录（已包含在仓库中）
	openZeppelinPath := filepath.Join(projectRoot, "eth", "solidity", "openzeppelin")

	// 构建 solc 命令参数
	// --allow-paths 支持多个路径，用逗号分隔
	allowPaths := "." + "," + openZeppelinPath

	// 添加 remapping 以便 solc 能找到 @openzeppelin 合约
	remapping := fmt.Sprintf("@openzeppelin=%s", openZeppelinPath)

	args := []string{
		"--allow-paths", allowPaths,
		remapping,
		"--optimize", "--optimize-runs", "200",
		"core/EntryPoint.sol",
		"--combined-json", "bin",
	}

	compileCmd := exec.Command("solc", args...)
	compileCmd.Dir = entryPointDir

	compileOutput, err := compileCmd.CombinedOutput()
	if err != nil {
		outputStr := string(compileOutput)
		// 检查是否是 OpenZeppelin 依赖缺失错误
		if strings.Contains(outputStr, "@openzeppelin") && strings.Contains(outputStr, "not found") {
			return common.Address{}, errors.Errorf("找不到 OpenZeppelin 合约\n请先安装依赖: npm install @openzeppelin/contracts\n\n编译错误详情:\n%s", outputStr)
		}
		return common.Address{}, errors.Wrapf(err, "编译 EntryPoint 失败: %s", outputStr)
	}

	// 解析编译输出（JSON）
	var compileResult map[string]interface{}
	if err := json.Unmarshal(compileOutput, &compileResult); err != nil {
		return common.Address{}, errors.Wrap(err, "解析编译输出失败")
	}

	contracts, ok := compileResult["contracts"].(map[string]interface{})
	if !ok {
		return common.Address{}, errors.New("编译输出格式错误")
	}

	entryPointKey := "core/EntryPoint.sol:EntryPoint"
	contract, ok := contracts[entryPointKey].(map[string]interface{})
	if !ok {
		return common.Address{}, errors.New("找不到 EntryPoint 合约")
	}

	bytecodeStr, ok := contract["bin"].(string)
	if !ok || bytecodeStr == "" {
		return common.Address{}, errors.New("无法获取 EntryPoint 字节码")
	}

	// 部署字节码
	bytecode := common.FromHex(bytecodeStr)
	address, tx, _, err := bind.DeployContract(auth, abi.ABI{}, bytecode, client)
	if err != nil {
		return common.Address{}, errors.Wrap(err, "部署 EntryPoint 字节码失败")
	}

	// 等待交易确认
	receipt, err := bind.WaitMined(context.Background(), client, tx)
	if err != nil {
		return common.Address{}, errors.Wrap(err, "等待交易确认失败")
	}

	if receipt.Status == types.ReceiptStatusFailed {
		return common.Address{}, errors.New("EntryPoint 部署交易失败")
	}

	return address, nil
}

func deployFactory(client *ethclient.Client, auth *bind.TransactOpts, owner common.Address, entryPoint common.Address, rpcURL string, gasLimit uint64, gasPrice *big.Int, privateKey *ecdsa.PrivateKey) (common.Address, error) {
	projectRoot, err := getProjectRoot()
	if err != nil {
		return common.Address{}, err
	}

	factoryFile := filepath.Join(projectRoot, "eth", "solidity", "LightAccountFactory.flatten.sol")

	// 检查文件是否存在
	if _, err := os.Stat(factoryFile); os.IsNotExist(err) {
		return common.Address{}, errors.Errorf("找不到 LightAccountFactory.flatten.sol 文件: %s", factoryFile)
	}

	// 使用 etherman 部署
	cmd := exec.Command("etherman",
		"-N", "LightAccountFactory",
		"-S", factoryFile,
		"-P", hex.EncodeToString(crypto.FromECDSA(privateKey)),
		"-E", rpcURL,
		"-G", fmt.Sprintf("%d", gasLimit),
		"-L", gasPrice.String(),
		"deploy", owner.Hex(), entryPoint.Hex(),
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return common.Address{}, errors.Wrapf(err, "etherman 执行失败: %s", string(output))
	}

	// 从输出中提取地址
	outputStr := string(output)
	lines := strings.Split(outputStr, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "0x") && len(line) >= 42 {
			addr := common.HexToAddress(strings.TrimSpace(line))
			if addr != (common.Address{}) {
				return addr, nil
			}
		}
	}

	// 尝试从输出中提取地址（更宽松的匹配）
	for _, line := range lines {
		if idx := strings.Index(line, "0x"); idx >= 0 {
			addrStr := line[idx:]
			if len(addrStr) >= 42 {
				addrStr = addrStr[:42]
				addr := common.HexToAddress(addrStr)
				if addr != (common.Address{}) {
					return addr, nil
				}
			}
		}
	}

	return common.Address{}, errors.Errorf("无法从输出中提取合约地址: %s", outputStr)
}
