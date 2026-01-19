package payload

import (
	"context"
	"math/big"
	"strings"
	"sync"
	"time"

	evmtypes "github.com/InjectiveLabs/sdk-go/chain/evm/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	ethcmn "github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/pkg/errors"
	"github.com/xlab/pace"
	log "github.com/xlab/suplog"

	"github.com/biya-coin/chain-stresser/v2/eth/aa"
)

var _ TxProvider = &ethERC20UserOpProvider{}

type ethERC20UserOpProvider struct {
	ethTxBuilderAndSigner

	minGasPrice          sdk.Coin
	maxGasLimitPerUserOp uint64
	maxGasLimitInitialTx uint64

	ethRPCURL string
	chainID   int64

	entrypoint            aa.Entrypoint
	entrypointAddress     ethcmn.Address
	accountFactoryAddress ethcmn.Address
	lightAccounts         map[int]aa.LightAccount
	lightAccountNonces    map[int]uint64
	nonceMutex            sync.Mutex
	accountsMutex         sync.RWMutex

	erc20ContractABI     *abi.ABI
	erc20ContractAddress ethcmn.Address
	recipientAddress     ethcmn.Address

	logger       log.Logger
	uoSignedPace pace.Pace
}

// NewEthERC20UserOpProvider creates transaction factory for stress testing
// ERC20 token transfers via UserOps (EIP-4337).
// Each transaction bundles multiple ERC20 transfer operations.
func NewEthERC20UserOpProvider(
	ethRPCURL string,
	ethChainID *big.Int,
	minGasPrice string,
	uoSignedPace pace.Pace,
	entrypointAddress,
	beneficiaryAddress,
	accountFactoryAddress,
	erc20ContractAddress,
	recipientAddress ethcmn.Address,
) (TxProvider, error) {
	parsedMinGasPrice, err := sdk.ParseCoinNormalized(minGasPrice)
	if err != nil {
		err = errors.Wrap(err, "failed to parse minGasPrice coin")
		return nil, err
	}

	// override minGasPrice if it's less than the initial base fee for EIP-1559 transactions
	if parsedMinGasPrice.Amount.LT(eip1559InitialBaseFee) {
		parsedMinGasPrice.Amount = eip1559InitialBaseFee
	}

	ethSigner := ethtypes.LatestSignerForChainID(ethChainID)

	provider := &ethERC20UserOpProvider{
		ethTxBuilderAndSigner: ethTxBuilderAndSigner{
			ethSigner: ethSigner,
			feeDenom:  parsedMinGasPrice.Denom,
		},

		minGasPrice:          parsedMinGasPrice,
		maxGasLimitPerUserOp: 150000,
		maxGasLimitInitialTx: 500000,

		ethRPCURL:             ethRPCURL,
		chainID:               ethChainID.Int64(),
		entrypointAddress:     entrypointAddress,
		accountFactoryAddress: accountFactoryAddress,

		lightAccounts:        make(map[int]aa.LightAccount),
		lightAccountNonces:   make(map[int]uint64),
		erc20ContractAddress: erc20ContractAddress,
		recipientAddress:     recipientAddress,

		uoSignedPace: uoSignedPace,
	}

	entrypoint, err := aa.NewEntrypoint(
		ethRPCURL,
		ethChainID.Int64(),
		accountFactoryAddress,
		beneficiaryAddress,
	)
	if err != nil {
		err = errors.Wrap(err, "failed to create entrypoint")
		return nil, err
	} else {
		provider.entrypoint = entrypoint
	}

	// ERC20 transfer function ABI: transfer(address to, uint256 amount)
	erc20ABIJSON := `[{"constant":false,"inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"type":"function"}]`
	contractABI, err := abi.JSON(strings.NewReader(erc20ABIJSON))
	if err != nil {
		err = errors.Wrap(err, "failed to parse ERC20 contract ABI")
		return nil, err
	} else {
		provider.erc20ContractABI = &contractABI
	}

	provider.logger = log.WithFields(log.Fields{
		"provider": provider.Name(),
	})

	return provider, nil
}

type ethERC20UserOpTx struct {
	baseTx

	to ethcmn.Address
}

func (p *ethERC20UserOpProvider) Name() string {
	return "eth_erc20_userop_stress"
}

func (p *ethERC20UserOpProvider) GenerateTx(
	req TxRequest,
) (Tx, error) {
	if p.erc20ContractAddress == (ethcmn.Address{}) {
		return nil, errors.New("erc20 contract address is not set")
	}

	// 转账金额：1 token (1e18 wei)
	transferAmount := big.NewInt(1e18)

	// 打包 transfer(recipient, amount) 调用数据
	erc20CallData, err := p.erc20ContractABI.Pack("transfer", p.recipientAddress, transferAmount)
	if err != nil {
		return nil, errors.Wrap(err, "failed to pack erc20 transfer calldata")
	}

	p.accountsMutex.RLock()
	lightAccount, ok := p.lightAccounts[req.FromIdx]
	p.accountsMutex.RUnlock()
	if !ok {
		return nil, errors.Errorf("light account not found for %d (%s)", req.FromIdx, req.From.Name)
	}

	personalSignFn := func(_ ethcmn.Address, data []byte) ([]byte, error) {
		pk, err := ethcrypto.ToECDSA([]byte(req.From.Key))
		if err != nil {
			return nil, err
		}

		digestHash := ethcrypto.Keccak256Hash(data)
		return ethcrypto.Sign(digestHash.Bytes(), pk)
	}

	signer := aa.NewSigner(
		p.entrypointAddress,
		p.ethSigner.ChainID(),
		personalSignFn,
		ethcmn.Address{}, // empty address ok since signerFn is fixed to privkey
	)

	// 每个交易捆绑 500 个 ERC20 转账操作
	opsPerTx := 500

	userOps := make([]aa.PackedUserOperation, opsPerTx)
	for i := 0; i < opsPerTx; i++ {
		p.nonceMutex.Lock()
		lightAccountNonce := p.lightAccountNonces[req.FromIdx]
		p.lightAccountNonces[req.FromIdx]++
		p.nonceMutex.Unlock()

		userOp, err := lightAccount.NewUserOperationWithNonce(
			p.erc20ContractAddress,
			erc20CallData,
			lightAccountNonce,
			aa.UserOperationGasEstimates{
				CallGasLimit:         120000,
				VerificationGasLimit: 200000,
				PreVerificationGas:   50000,
				MaxFeePerGas:         0,
				MaxPriorityFeePerGas: 0,
			},
		)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create user operation")
		}

		packedUO, _, err := signer.SignUserOperation(userOp)
		if err != nil {
			err = errors.Wrapf(err,
				"failed to sign user operation (idx: %d, account: %d, nonce: %d)",
				i, req.FromIdx, lightAccountNonce,
			)
			return nil, err
		}

		userOps[i] = *packedUO
		p.uoSignedPace.StepN(1)
	}

	handleOpsCallData, err := p.entrypoint.HandleOpsCallData(userOps, req.From.EthAddress())
	if err != nil {
		return nil, errors.Wrap(err, "failed to pack handleOps calldata")
	}

	tx := &ethERC20UserOpTx{
		baseTx: baseTx{
			from: req.From,
			msgs: []sdk.Msg{
				evmtypes.NewTxWithData(&ethtypes.LegacyTx{
					Nonce:    req.From.Sequence,
					To:       &p.entrypointAddress,
					Value:    noValue,
					Gas:      p.maxGasLimitPerUserOp * uint64(len(userOps)),
					GasPrice: p.minGasPrice.Amount.BigInt(),
					Data:     handleOpsCallData,
				}),
			},

			fromIdx: req.FromIdx,
			txIdx:   req.TxIdx,
		},

		to: p.entrypointAddress,
	}

	return tx, nil
}

func (p *ethERC20UserOpProvider) GenerateInitialTx(
	req TxRequest,
) (Tx, error) {
	eoaAddress := req.From.EthAddress()

	la, err := aa.NewLightAccount(
		p.ethRPCURL,
		p.chainID,
		p.entrypointAddress,
		p.accountFactoryAddress,
		eoaAddress,
		defaultAccountSalt,
	)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create light account (idx: %d; account: %s)", req.FromIdx, req.From.Name)
	}

	p.accountsMutex.Lock()
	p.lightAccounts[req.FromIdx] = la
	p.accountsMutex.Unlock()

	initCtx, cancelFn := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelFn()

	// 检查 Light Account 是否已部署（通过检查合约代码）
	_, err = la.Address(initCtx)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get light account address (idx: %d; account: %s)", req.FromIdx, req.From.Name)
	}

	code, err := la.Code(initCtx)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get light account code (idx: %d; account: %s)", req.FromIdx, req.From.Name)
	}
	isDeployed := len(code) > 0

	nonce, err := la.Nonce(initCtx, ethcmn.Big0)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to sync nonce for light account (idx: %d; account: %s)", req.FromIdx, req.From.Name)
	}

	p.nonceMutex.Lock()
	p.lightAccountNonces[req.FromIdx] = nonce
	p.nonceMutex.Unlock()

	// 如果 Light Account 已部署，检查余额是否充足
	if isDeployed {
		balance, err := la.Balance(initCtx)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get balance for light account (idx: %d; account: %s)", req.FromIdx, req.From.Name)
		}

		// 如果余额充足，不需要初始化交易
		if balance.Cmp(minAccountBalanceOnEndpoint) >= 0 {
			return nil, nil
		}

		// 余额不足，需要充值到 EntryPoint
		depositCallData, err := la.DepositToEntrypointCallData()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get deposit to entrypoint call data for light account (idx: %d; account: %s)", req.FromIdx, req.From.Name)
		}

		tx := &baseTx{
			from: req.From,
			msgs: []sdk.Msg{
				evmtypes.NewTxWithData(&ethtypes.LegacyTx{
					Nonce:    req.From.Sequence,
					To:       &p.entrypointAddress,
					Value:    endpointAccountBalanceTopup,
					Gas:      p.maxGasLimitInitialTx,
					GasPrice: p.minGasPrice.Amount.BigInt(),
					Data:     depositCallData,
				}),
			},

			fromIdx: req.FromIdx,
			txIdx:   req.TxIdx,
		}

		return tx, nil
	}

	// Light Account 未部署，第一个 UserOp 会自动部署（通过 initCode）
	// 仍然需要检查并充值 EntryPoint 余额
	balance, err := la.Balance(initCtx)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get balance for light account (idx: %d; account: %s)", req.FromIdx, req.From.Name)
	}

	if balance.Cmp(minAccountBalanceOnEndpoint) >= 0 {
		return nil, nil
	}

	depositCallData, err := la.DepositToEntrypointCallData()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get deposit to entrypoint call data for light account (idx: %d; account: %s)", req.FromIdx, req.From.Name)
	}

	tx := &baseTx{
		from: req.From,
		msgs: []sdk.Msg{
			evmtypes.NewTxWithData(&ethtypes.LegacyTx{
				Nonce:    req.From.Sequence,
				To:       &p.entrypointAddress,
				Value:    endpointAccountBalanceTopup,
				Gas:      p.maxGasLimitInitialTx,
				GasPrice: p.minGasPrice.Amount.BigInt(),
				Data:     depositCallData,
			}),
		},

		fromIdx: req.FromIdx,
		txIdx:   req.TxIdx,
	}

	return tx, nil
}
