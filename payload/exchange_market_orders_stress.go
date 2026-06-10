package payload

import (
	"fmt"
	"math/big"
	"math/rand"
	"time"

	"cosmossdk.io/math"
	exchangev2types "github.com/InjectiveLabs/sdk-go/chain/exchange/types/v2"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/pkg/errors"
	log "github.com/xlab/suplog"

	"github.com/biya-coin/chain-stresser/v2/chain"
)

var _ TxProvider = &exchangeMarketOrdersProvider{}
var _ PreStressTxProvider = &exchangeMarketOrdersProvider{}

type exchangeMarketOrdersProvider struct {
	spotMarketIDs       []string
	derivativeMarketIDs []string
	ordersPerMarket     int
	minGasPrice         sdk.Coin
	maxGasLimit         uint64
	memoAttach          string
	makerKey            chain.Secp256k1PrivateKey
	depositDenoms       map[string]math.Int
	logger              log.Logger
}

// NewExchangeMarketOrdersProvider creates transaction factory for stress testing
// exchange market orders via MsgBatchUpdateOrders.
// makerKey 是做市账户私钥（必须提供，从 validators/staker_keys.json 读取第一个 key）。
func NewExchangeMarketOrdersProvider(
	minGasPrice string,
	spotMarketIDs []string,
	derivativeMarketIDs []string,
	ordersPerMarket int,
	makerKey *chain.Secp256k1PrivateKey,
) (TxProvider, error) {
	if makerKey == nil {
		return nil, errors.New("makerKey is required: ensure validators/staker_keys.json exists and contains at least one key")
	}

	parsedMinGasPrice, err := sdk.ParseCoinNormalized(minGasPrice)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse minGasPrice coin")
	}

	if ordersPerMarket <= 0 {
		ordersPerMarket = 1
	}

	// 压测账号子账号重置：
	//   10,000,000 BYB = 1e25 byb
	//   10,000,000 USDT = 1e13 usdt
	depositDenoms := map[string]math.Int{
		"byb": math.NewIntFromBigInt(
			new(big.Int).Mul(big.NewInt(1), new(big.Int).Exp(big.NewInt(10), big.NewInt(25), nil)),
		),
		"peggy0x3de4027B5b0Bf278Db2D187768AC441e9B356360": math.NewInt(10_000_000_000_000),
	}

	provider := &exchangeMarketOrdersProvider{
		spotMarketIDs:       spotMarketIDs,
		derivativeMarketIDs: derivativeMarketIDs,
		ordersPerMarket:     ordersPerMarket,
		minGasPrice:         parsedMinGasPrice,
		maxGasLimit:         75000000,
		depositDenoms:       depositDenoms,
		makerKey:            *makerKey,
	}

	provider.logger = log.WithFields(log.Fields{
		"provider": provider.Name(),
	})

	return provider, nil
}

type exchangeMarketOrderTx struct {
	baseTx
}

func (p *exchangeMarketOrdersProvider) Name() string {
	return "exchange_market_orders_stress"
}

// GenerateInitialTx 在压测正式开始前对每个账户调用一次（await=true）。
// 发送多笔 MsgDeposit，将 byb 和 peggy 充值到账户的默认子账户，
func (p *exchangeMarketOrdersProvider) GenerateInitialTx(
	req TxRequest,
) (Tx, error) {
	sender := req.From.Key.AccAddress()
	// index=1：MsgDeposit 不允许充值到 default subaccount (index=0)
	defaultSubaccountID := subaccount(req.From.Key.Address(), 1).Hex()

	var msgs []sdk.Msg
	for denom, amount := range p.depositDenoms {
		msgs = append(msgs, &exchangev2types.MsgDeposit{
			Sender:       string(sender),
			SubaccountId: defaultSubaccountID,
			Amount:       sdk.NewCoin(denom, amount),
		})

		p.logger.WithFields(log.Fields{
			"sender":     string(sender),
			"subaccount": defaultSubaccountID,
			"denom":      denom,
			"amount":     amount.String(),
		}).Info("💰 Depositing to subaccount before stress test")
	}

	if len(msgs) == 0 {
		return nil, nil
	}

	tx := &exchangeMarketOrderTx{
		baseTx: baseTx{
			from:    req.From,
			msgs:    msgs,
			fromIdx: req.FromIdx,
			txIdx:   0,
		},
	}

	return tx, nil
}

// GeneratePreStressTx 在 MsgDeposit 确认上链后、压测正式开始前由 stresser 调用（每个压测账户调用一次）。
// 只在 FromIdx==0 时执行（使用 makerKey 账户）：
//  1. 向 exchange 子账户充值（MsgDeposit byb + usdt）
//  2. 挂做市限价单（MsgBatchUpdateOrders BUY@0.01 + SELL@10.00）
//
// 两条消息打包进同一笔 tx，Cosmos SDK 顺序执行，充值在挂单前生效。
func (p *exchangeMarketOrdersProvider) GeneratePreStressTx(
	req TxRequest,
) (Tx, error) {
	// 只让第一个压测账户触发做市单（实际使用 makerKey 账户）
	if req.FromIdx != 0 {
		return nil, nil
	}

	senderKey := p.makerKey
	// Number/Sequence 留零值，createAndBroadcastPreStressTxs 会检测 key 不同并重新查询
	from := chain.Account{
		Name: "maker",
		Key:  senderKey,
	}

	sender := senderKey.AccAddress()
	// subaccount index=1；index=0 是默认子账户，MsgDeposit 不可直接充值
	makerSubaccountID := subaccount(senderKey.Address(), 1).Hex()

	// 做市单数量：设为 10 亿（10^9），validator 账户余额充足，永不耗尽。
	// 价格区间不交叉，双侧同时挂盘：
	//   BUY @0.01  → 匹配 SELL 市价单（价格 0.001~0.005，低于 BUY@0.01）
	//   SELL@10.00 → 匹配 BUY  市价单（价格 11.0~20.0，高于 SELL@10.00）
	makerQtyInt := new(big.Int).Exp(big.NewInt(10), big.NewInt(9), nil)       // 10^9
	makerQuantity := math.LegacyNewDecFromBigInt(makerQtyInt)                 // 1,000,000,000
	makerBuyPrice := math.LegacyNewDecFromIntWithPrec(math.NewInt(10), 3)     // 0.010
	makerSellPrice := math.LegacyNewDecFromIntWithPrec(math.NewInt(10000), 3) // 10.000

	// ---------------------------------------------------------------
	// 消息列表：
	//   [0] MsgDeposit byb  → 覆盖 SELL@10 × qty=10^9 所需 byb（SELL 限价单冻结 BASE token）
	//   [1] MsgDeposit usdt → 覆盖 BUY@0.01 × qty=10^9 所需 usdt（BUY 限价单冻结 QUOTE token）
	//   [2] MsgBatchUpdateOrders → 挂双侧做市限价单
	// ---------------------------------------------------------------
	numMarkets := int64(len(p.spotMarketIDs))
	if numMarkets == 0 {
		return nil, nil
	}

	// byb deposit: SELL@10 限价单冻结的是 BASE token（byb），金额 = quantity
	//   = qty=10^9 × numMarkets × 2 (safety) BYB（18 decimals → ×10^18）
	//   注意：SELL 限价单需要 BASE，不是 price×qty（那是 BUY 侧需要的 QUOTE）
	bybDepositAmt := math.NewIntFromBigInt(new(big.Int).Mul(
		new(big.Int).Mul(big.NewInt(2_000_000_000), big.NewInt(numMarkets)), // 2×10^9 × numMarkets
		new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil),               // × 10^18 base units
	))
	// usdt deposit: BUY@0.01 限价单冻结的是 QUOTE token（usdt），金额 = price × quantity
	//   = 0.01 × 10^9 × 2 × numMarkets USDT = 2×10^7 × numMarkets USDT（6 decimals → ×10^6）
	usdtDepositAmt := math.NewIntFromBigInt(new(big.Int).Mul(
		new(big.Int).Mul(big.NewInt(20_000_000), big.NewInt(numMarkets)), // 2×10^7 × numMarkets
		new(big.Int).Exp(big.NewInt(10), big.NewInt(6), nil),             // × 10^6 base units
	))

	var msgs []sdk.Msg

	msgs = append(msgs,
		&exchangev2types.MsgDeposit{
			Sender:       sender,
			SubaccountId: makerSubaccountID,
			Amount:       sdk.NewCoin("byb", bybDepositAmt),
		},
		&exchangev2types.MsgDeposit{
			Sender:       sender,
			SubaccountId: makerSubaccountID,
			Amount:       sdk.NewCoin("peggy0x3de4027B5b0Bf278Db2D187768AC441e9B356360", usdtDepositAmt),
		},
	)

	var spotOrders []*exchangev2types.SpotOrder
	for i, marketID := range p.spotMarketIDs {
		spotOrders = append(spotOrders,
			&exchangev2types.SpotOrder{
				MarketId:  marketID,
				OrderType: exchangev2types.OrderType_BUY,
				OrderInfo: exchangev2types.OrderInfo{
					FeeRecipient: sender,
					Price:        makerBuyPrice,
					Quantity:     makerQuantity,
					Cid:          fmt.Sprintf("mk-buy-%d", i),
					SubaccountId: makerSubaccountID,
				},
			},
			&exchangev2types.SpotOrder{
				MarketId:  marketID,
				OrderType: exchangev2types.OrderType_SELL,
				OrderInfo: exchangev2types.OrderInfo{
					FeeRecipient: sender,
					Price:        makerSellPrice,
					Quantity:     makerQuantity,
					Cid:          fmt.Sprintf("mk-sell-%d", i),
					SubaccountId: makerSubaccountID,
				},
			},
		)

		p.logger.WithFields(log.Fields{
			"maker_account": sender,
			"subaccount":    makerSubaccountID,
			"buy_price":     makerBuyPrice.String(),
			"sell_price":    makerSellPrice.String(),
			"quantity":      makerQuantity.String(),
			"market_id":     marketID,
		}).Info("📋 Pre-stress: deposit + place maker limit orders")
	}

	msgs = append(msgs, &exchangev2types.MsgBatchUpdateOrders{
		Sender:             sender,
		SpotOrdersToCreate: spotOrders,
	})

	tx := &exchangeMarketOrderTx{
		baseTx: baseTx{
			from:    from,
			msgs:    msgs,
			fromIdx: req.FromIdx,
			txIdx:   0,
		},
	}

	return tx, nil
}

func (p *exchangeMarketOrdersProvider) GenerateTx(
	req TxRequest,
) (Tx, error) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	sender := req.From.Key.AccAddress()
	// index=1：与 GenerateInitialTx 充值目标保持一致
	defaultSubaccountID := subaccount(req.From.Key.Address(), 1).Hex()

	var msgs []sdk.Msg

	// ---------------------------------------------------------------
	// 现货市价单：直接发 MsgCreateSpotMarketOrder，确保走
	// SpotMsgServer.CreateSpotMarketOrder 入口，触发 Prometheus 指标。
	//
	// 做市限价单已由 GeneratePreStressTx 提前确认上链：
	//   BUY  maker @0.01  → 匹配 SELL 市价单（价格 0.001~0.005）
	//   SELL maker @10.00 → 匹配 BUY  市价单（价格 11.0~20.0）
	// ---------------------------------------------------------------
	for marketIdx, marketID := range p.spotMarketIDs {
		for orderIdx := 0; orderIdx < p.ordersPerMarket; orderIdx++ {
			// 数量：0.001 ~ 0.010（极小，确保不耗尽做市单）
			quantity := math.LegacyNewDecFromIntWithPrec(math.NewInt(r.Int63n(10)+1), 3)

			var orderType exchangev2types.OrderType
			var spotPrice math.LegacyDec

			if r.Intn(2) == 0 {
				// BUY 市价单：价格 11.000 ~ 20.000（高于 SELL@10 做市单，必然撮合）
				orderType = exchangev2types.OrderType_BUY
				spotPriceValue := int64(r.Int63n(9001) + 11000)
				spotPrice = math.LegacyNewDecFromIntWithPrec(math.NewInt(spotPriceValue), 3)
			} else {
				// SELL 市价单：价格 0.001 ~ 0.005（低于 BUY@0.01 做市单，必然撮合）
				orderType = exchangev2types.OrderType_SELL
				spotPriceValue := int64(r.Int63n(5) + 1)
				spotPrice = math.LegacyNewDecFromIntWithPrec(math.NewInt(spotPriceValue), 3)
			}

			cid := fmt.Sprintf("m-%d-%d-%d-%d", req.FromIdx, req.TxIdx, marketIdx, orderIdx)

			msg := &exchangev2types.MsgCreateSpotMarketOrder{
				Sender: string(sender),
				Order: exchangev2types.SpotOrder{
					MarketId:  string(marketID),
					OrderType: orderType,
					OrderInfo: exchangev2types.OrderInfo{
						FeeRecipient: string(sender),
						Price:        spotPrice,
						Quantity:     quantity,
						Cid:          cid,
						SubaccountId: defaultSubaccountID,
					},
				},
			}

			p.logger.WithFields(log.Fields{
				"order_type": orderType.String(),
				"price":      spotPrice.String(),
				"quantity":   quantity.String(),
				"market_id":  marketID,
				"cid":        cid,
			}).Debug("📝 Creating spot market order via MsgCreateSpotMarketOrder")

			msgs = append(msgs, msg)
		}
	}

	// 衍生品市价单（如有）仍通过 MsgBatchUpdateOrders
	if len(p.derivativeMarketIDs) > 0 {
		batchMsg := &exchangev2types.MsgBatchUpdateOrders{
			Sender:                         string(sender),
			DerivativeMarketOrdersToCreate: []*exchangev2types.DerivativeOrder{},
		}

		for i, marketID := range p.derivativeMarketIDs {
			for orderIdx := 0; orderIdx < p.ordersPerMarket; orderIdx++ {
				derivativePriceValue := int64(r.Int63n(10001) + 50001)
				derivativePrice := math.LegacyNewDecFromIntWithPrec(math.NewInt(derivativePriceValue), 3)
				quantity := math.LegacyNewDecFromIntWithPrec(math.NewInt(r.Int63n(10000)+1), 3)

				cid := fmt.Sprintf("m-%d-%d-%d-%d", req.FromIdx, req.TxIdx, i, orderIdx)

				var derivativeOrderType exchangev2types.OrderType
				if r.Intn(2) == 0 {
					derivativeOrderType = exchangev2types.OrderType_BUY_ATOMIC
				} else {
					derivativeOrderType = exchangev2types.OrderType_SELL_ATOMIC
				}

				batchMsg.DerivativeMarketOrdersToCreate = append(batchMsg.DerivativeMarketOrdersToCreate, &exchangev2types.DerivativeOrder{
					MarketId:  string(marketID),
					OrderType: derivativeOrderType,
					Margin:    derivativePrice.Mul(quantity),
					OrderInfo: exchangev2types.OrderInfo{
						FeeRecipient: string(sender),
						Price:        derivativePrice,
						Quantity:     quantity,
						Cid:          cid,
						SubaccountId: defaultSubaccountID,
					},
				})
			}
		}

		msgs = append(msgs, batchMsg)
	}

	if len(msgs) == 0 {
		return nil, errors.New("no market IDs configured")
	}

	tx := &exchangeMarketOrderTx{
		baseTx: baseTx{
			from:    req.From,
			msgs:    msgs,
			fromIdx: req.FromIdx,
			txIdx:   req.TxIdx,
		},
	}

	return tx, nil
}

func (p *exchangeMarketOrdersProvider) BuildAndSignTx(
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
