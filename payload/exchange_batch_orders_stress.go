package payload

import (
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"time"

	"cosmossdk.io/math"
	exchangev2types "github.com/InjectiveLabs/sdk-go/chain/exchange/types/v2"
	sdk "github.com/cosmos/cosmos-sdk/types"
	eth "github.com/ethereum/go-ethereum/common"
	"github.com/pkg/errors"
	log "github.com/xlab/suplog"

	"github.com/biya-coin/chain-stresser/v2/chain"
)

var _ TxProvider = &exchangeBatchOrdersProvider{}

type exchangeBatchOrdersProvider struct {
	spotMarketIDs       []string
	derivativeMarketIDs []string
	numTargets          int
	ordersPerMarket     int
	minGasPrice         sdk.Coin
	maxGasLimit         uint64
	memoAttach          string
	depositDenoms       map[string]math.Int
	logger              log.Logger
}

// NewExchangeBatchUpdateProvider creates transaction factory for stress testing
// exchange batch updates.
func NewExchangeBatchOrdersProvider(
	minGasPrice string,
	spotMarketIDs []string,
	derivativeMarketIDs []string,
	ordersPerMarket int,
) (TxProvider, error) {

	parsedMinGasPrice, err := sdk.ParseCoinNormalized(minGasPrice)
	if err != nil {
		err = errors.Wrap(err, "failed to parse minGasPrice coin")
		return nil, err
	}

	if ordersPerMarket <= 0 {
		ordersPerMarket = 1
	}

	// 非默认子账户(index=1)交易前预充值的金额，远超压测下单所需：
	//   10,000,000 BYB  = 1e25 byb（base，现货 SELL 单冻结）
	//   10,000,000 USDT = 1e13 usdt（quote，现货 BUY / 衍生品 margin 冻结）
	depositDenoms := map[string]math.Int{
		"byb": math.NewIntFromBigInt(
			new(big.Int).Mul(big.NewInt(1), new(big.Int).Exp(big.NewInt(10), big.NewInt(25), nil)),
		),
		"peggy0x3de4027B5b0Bf278Db2D187768AC441e9B356360": math.NewInt(10_000_000_000_000),
	}

	provider := &exchangeBatchOrdersProvider{
		spotMarketIDs:       spotMarketIDs,
		derivativeMarketIDs: derivativeMarketIDs,
		ordersPerMarket:     ordersPerMarket,
		minGasPrice:         parsedMinGasPrice,
		maxGasLimit:         75000000,
		depositDenoms:       depositDenoms,
	}

	provider.logger = log.WithFields(log.Fields{
		"provider": provider.Name(),
	})

	return provider, nil
}

type exchangeBatchUpdateTx struct {
	baseTx
}

func (p *exchangeBatchOrdersProvider) Name() string {
	return "exchange_batch_orders_stress"
}

func (p *exchangeBatchOrdersProvider) GenerateTx(
	req TxRequest,
) (Tx, error) {
	// Use standard random generation
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	// 随机生成数量：0.001 ~ 100.000
	quantity := math.LegacyNewDecFromIntWithPrec(math.NewInt(r.Int63n(100000)+1), 3)

	sender := req.From.Key.AccAddress()
	// index=1：非默认子账户，资金在交易所内部 deposit 账本，下单不触碰 bank
	subaccountID := subaccount(req.From.Key.Address(), 1).Hex()

	msg := exchangev2types.MsgBatchUpdateOrders{
		Sender:                         string(sender),
		SubaccountId:                   "",
		SpotOrdersToCreate:             []*exchangev2types.SpotOrder{},
		DerivativeOrdersToCreate:       []*exchangev2types.DerivativeOrder{},
		SpotMarketIdsToCancelAll:       []string{}, // 空数组，不取消订单
		DerivativeMarketIdsToCancelAll: []string{}, // 空数组，不取消订单
	}

	for i, marketID := range p.derivativeMarketIDs {
		for orderIdx := 0; orderIdx < p.ordersPerMarket; orderIdx++ {
			// 随机生成价格：50.001 ~ 60.001
			derivativePriceValue := int64(r.Int63n(10001) + 50001)
			derivativePrice := math.LegacyNewDecFromIntWithPrec(math.NewInt(derivativePriceValue), 3)

			cid := fmt.Sprintf("%d-%d-%d-%d", req.FromIdx, req.TxIdx, i, orderIdx)
			// 随机选择订单类型
			var derivativeOrderType exchangev2types.OrderType
			if r.Intn(2) == 0 {
				derivativeOrderType = exchangev2types.OrderType_BUY
			} else {
				derivativeOrderType = exchangev2types.OrderType_SELL
			}

			derivativeOrder := &exchangev2types.DerivativeOrder{
				MarketId:  string(marketID),
				OrderType: derivativeOrderType,
				Margin:    derivativePrice.Mul(quantity),
				OrderInfo: exchangev2types.OrderInfo{
					FeeRecipient: string(sender),
					Price:        derivativePrice,
					Quantity:     quantity,
					Cid:          cid,
					SubaccountId: subaccountID,
				},
			}

			p.logger.WithFields(log.Fields{
				"order_type": derivativeOrderType.String(),
				"price":      derivativePrice.String(),
				"quantity":   quantity.String(),
			}).Debug("📝 Creating derivative order")

			msg.DerivativeOrdersToCreate = append(msg.DerivativeOrdersToCreate, derivativeOrder)
		}
	}

	for i, marketID := range p.spotMarketIDs {
		for orderIdx := 0; orderIdx < p.ordersPerMarket; orderIdx++ {
			// 随机生成价格：50.001 ~ 60.000
			spotPriceValue := int64(r.Int63n(10001) + 50001)
			spotPrice := math.LegacyNewDecFromIntWithPrec(math.NewInt(spotPriceValue), 3)

			cid := fmt.Sprintf("%d-%d-%d-%d", req.FromIdx, req.TxIdx, i, orderIdx)

			// 随机订单类型
			var spotOrderType exchangev2types.OrderType
			if r.Intn(2) == 0 {
				spotOrderType = exchangev2types.OrderType_BUY
			} else {
				spotOrderType = exchangev2types.OrderType_SELL
			}

			spotOrder := &exchangev2types.SpotOrder{
				MarketId:  string(marketID),
				OrderType: spotOrderType,
				OrderInfo: exchangev2types.OrderInfo{
					FeeRecipient: string(sender),
					Price:        spotPrice,
					Quantity:     quantity,
					Cid:          cid,
					SubaccountId: subaccountID,
				},
			}

			p.logger.WithFields(log.Fields{
				"order_type": spotOrderType.String(),
				"price":      spotPrice.String(),
				"quantity":   quantity.String(),
			}).Debug("📝 Creating spot order")

			msg.SpotOrdersToCreate = append(msg.SpotOrdersToCreate, spotOrder)
		}
	}

	tx := &exchangeBatchUpdateTx{
		baseTx: baseTx{
			from:    req.From,
			msgs:    []sdk.Msg{&msg},
			fromIdx: req.FromIdx,
			txIdx:   req.TxIdx,
		},
	}

	return tx, nil
}

func (p *exchangeBatchOrdersProvider) BuildAndSignTx(
	client chain.Client,
	unsignedTx Tx,
) (signedTx Tx, err error) {
	chainTx := chain.Tx{
		Msgs:     unsignedTx.Msgs(),
		GasLimit: p.maxGasLimit,
		Fee:      sdk.Coins{},
		Memo:     p.memoAttach,
	}

	signedResult, err := client.BuildAndSignTx(unsignedTx.From(), chainTx)
	if err != nil {
		return nil, err
	}

	tx := unsignedTx.WithBytes(client.Encode(signedResult))
	return tx, nil
}

// GenerateInitialTx 在压测正式开始前对每个账户调用一次（await=true）。
// 发送多笔 MsgDeposit，把 byb / usdt 充值到账户的【非默认子账户 index=1】，
// 使下单/撮合/结算全程走交易所内部 deposit 账本，不触碰 bank 模块。
func (p *exchangeBatchOrdersProvider) GenerateInitialTx(
	req TxRequest,
) (Tx, error) {
	sender := req.From.Key.AccAddress()
	// index=1：MsgDeposit 不允许充值到 default subaccount (index=0)
	subaccountID := subaccount(req.From.Key.Address(), 1).Hex()

	var msgs []sdk.Msg
	for denom, amount := range p.depositDenoms {
		msgs = append(msgs, &exchangev2types.MsgDeposit{
			Sender:       string(sender),
			SubaccountId: subaccountID,
			Amount:       sdk.NewCoin(denom, amount),
		})

		p.logger.WithFields(log.Fields{
			"sender":     string(sender),
			"subaccount": subaccountID,
			"denom":      denom,
			"amount":     amount.String(),
		}).Info("💰 Depositing to non-default subaccount before stress test")
	}

	if len(msgs) == 0 {
		return nil, nil
	}

	tx := &exchangeBatchUpdateTx{
		baseTx: baseTx{
			from:    req.From,
			msgs:    msgs,
			fromIdx: req.FromIdx,
			txIdx:   0,
		},
	}

	return tx, nil
}

func subaccount(account sdk.AccAddress, index int) eth.Hash {
	ethAddress := eth.BytesToAddress(account.Bytes())
	ethLowerAddress := strings.ToLower(ethAddress.String())

	subaccountId := fmt.Sprintf("%s%024x", ethLowerAddress, index)
	return eth.HexToHash(subaccountId)
}
