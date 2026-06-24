package payload

import (
	"fmt"
	"math/big"
	"math/rand"
	"time"

	"github.com/google/uuid"

	"cosmossdk.io/math"
	exchangev2types "github.com/InjectiveLabs/sdk-go/chain/exchange/types/v2"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/pkg/errors"
	log "github.com/xlab/suplog"

	"github.com/biya-coin/chain-stresser/v2/chain"
)

var _ TxProvider = &exchangeSpotLimitOrdersProvider{}

type exchangeSpotLimitOrdersProvider struct {
	spotMarketIDs   []string
	ordersPerMarket int
	minGasPrice     sdk.Coin
	maxGasLimit     uint64
	memoAttach      string
	depositDenoms   map[string]math.Int
	logger          log.Logger
}

// NewExchangeSpotLimitOrdersProvider creates a TxProvider that sends
// MsgCreateSpotLimitOrder directly, bypassing the batch interface.
// This ensures SpotMsgServer.CreateSpotLimitOrder is called and Prometheus
// metrics/blockStepAcc counters are properly recorded.
func NewExchangeSpotLimitOrdersProvider(
	minGasPrice string,
	spotMarketIDs []string,
	ordersPerMarket int,
) (TxProvider, error) {
	parsedMinGasPrice, err := sdk.ParseCoinNormalized(minGasPrice)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse minGasPrice coin")
	}

	if ordersPerMarket <= 0 {
		ordersPerMarket = 1
	}

	depositDenoms, err := spotLimitDepositDenoms(spotMarketIDs)
	if err != nil {
		return nil, err
	}

	provider := &exchangeSpotLimitOrdersProvider{
		spotMarketIDs:   spotMarketIDs,
		ordersPerMarket: ordersPerMarket,
		minGasPrice:     parsedMinGasPrice,
		maxGasLimit:     800000,
		depositDenoms:   depositDenoms,
	}

	provider.logger = log.WithFields(log.Fields{
		"provider": provider.Name(),
	})

	return provider, nil
}

type spotLimitMarketDenoms struct {
	base  string
	quote string
}

var spotLimitKnownMarkets = map[string]spotLimitMarketDenoms{
	"0xb322bce686ec25364be50728812e33741da1d82e9c91c2c89b91b91d26b0e9c5": {
		base:  "byb",
		quote: "peggy0x3de4027B5b0Bf278Db2D187768AC441e9B356360",
	},
	"0x754e40e2ad281abfe097072e7f266a2526a1180986ee7dbdf0ed9b85ffcff197": {
		base:  "byb",
		quote: "peggy0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238",
	},
	"0x675762bbc9e772197c6ca5deeea18d98f36c254e315d17a9fe0fc667c947cc03": {
		base:  "peggy0xB62132e35a6c13ee1EE0f84dC5d40bad8d815206",
		quote: "peggy0x3de4027B5b0Bf278Db2D187768AC441e9B356360",
	},
	"0x2bc2d9cc26226b8cfc2fe0112c9ca34a3226c59d9f2ea1003fa1f0d01d0b6920": {
		base:  "peggy0xB62132e35a6c13ee1EE0f84dC5d40bad8d815206",
		quote: "peggy0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238",
	},
	"0xd084cb604a71fc447ef54a31bc5f73b6e9e99dd23b7c4e70794cb3d2e21b16cc": {
		base:  "peggy0xa8c8CfB141A3bB59FEA1E2ea6B79b5ECBCD7b6ca",
		quote: "peggy0x3de4027B5b0Bf278Db2D187768AC441e9B356360",
	},
	"0xeb8abf12402a28a6636b12ad9f6b86c4af4d2de077e254fd57b01b980febcdd9": {
		base:  "peggy0xa8c8CfB141A3bB59FEA1E2ea6B79b5ECBCD7b6ca",
		quote: "peggy0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238",
	},
	"0x6246a12dd083039f1e073cb75c0a6179da74ea6f2bcdb9f178e071c45057d469": {
		base:  "peggy0x967da4048cD07aB37855c090aAF366e4ce1b9F48",
		quote: "peggy0x3de4027B5b0Bf278Db2D187768AC441e9B356360",
	},
	"0xe37754a183987825a0d40c1a85570cca19cab15dd4bb54f87036a5ffbbf742cf": {
		base:  "peggy0x967da4048cD07aB37855c090aAF366e4ce1b9F48",
		quote: "peggy0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238",
	},
	"0x181ce56d07b29b81d518bd98d06a6af30b7343e3782fb8a6bff79d72b52a72d7": {
		base:  "peggy0x2260FAC5E5542a773Aa44fBCfeDf7C193bc2C599",
		quote: "peggy0x3de4027B5b0Bf278Db2D187768AC441e9B356360",
	},
	"0x7c9e6539b5d0ddfd9993948e54f1cec8ac2d0330e76851fd3d78d4fdccbf20ec": {
		base:  "peggy0x2260FAC5E5542a773Aa44fBCfeDf7C193bc2C599",
		quote: "peggy0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238",
	},
	"0x13383dad4f39d8ffa52342189a3cff4a7f414d87d83ab3f37e46aa5e1dbe7970": {
		base:  "peggy0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2",
		quote: "peggy0x3de4027B5b0Bf278Db2D187768AC441e9B356360",
	},
	"0xe265de61063726675da1d6dbf0dc0360fe9ba96e8d6499f8aa5016f451bc41f3": {
		base:  "peggy0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2",
		quote: "peggy0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238",
	},
}

func spotLimitDepositDenoms(spotMarketIDs []string) (map[string]math.Int, error) {
	if len(spotMarketIDs) == 0 {
		return nil, errors.New("no spot market IDs configured")
	}

	depositDenoms := make(map[string]math.Int, len(spotMarketIDs)*2)
	for _, marketID := range spotMarketIDs {
		denoms, ok := spotLimitKnownMarkets[marketID]
		if !ok {
			return nil, errors.Errorf("unknown spot market ID %s; add its base/quote denoms to spotLimitKnownMarkets", marketID)
		}
		depositDenoms[denoms.base] = spotLimitDepositAmount(denoms.base)
		depositDenoms[denoms.quote] = spotLimitDepositAmount(denoms.quote)
	}

	return depositDenoms, nil
}

func spotLimitDepositAmount(denom string) math.Int {
	switch denom {
	case "byb":
		return math.NewIntFromBigInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(25), nil))
	case "peggy0x3de4027B5b0Bf278Db2D187768AC441e9B356360",
		"peggy0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238":
		return math.NewInt(10_000_000_000_000)
	default:
		return math.NewIntFromBigInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(23), nil))
	}
}

type exchangeSpotLimitOrderTx struct {
	baseTx
}

func (p *exchangeSpotLimitOrdersProvider) Name() string {
	return "exchange_spot_limit_orders_stress"
}

// GenerateInitialTx 在压测正式开始前对每个账户调用一次（await=true）。
// 发送多笔 MsgDeposit，把 byb / usdt 充值到账户的【非默认子账户 index=1】，
// 这样下单/撮合/结算全程走交易所内部 deposit 账本，不触碰 bank 模块。
func (p *exchangeSpotLimitOrdersProvider) GenerateInitialTx(req TxRequest) (Tx, error) {
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

		// p.logger.WithFields(log.Fields{
		// 	"sender":     string(sender),
		// 	"subaccount": subaccountID,
		// 	"denom":      denom,
		// 	"amount":     amount.String(),
		// }).Info("💰 Depositing to non-default subaccount before stress test")
	}

	if len(msgs) == 0 {
		return nil, nil
	}

	tx := &exchangeSpotLimitOrderTx{
		baseTx: baseTx{
			from:    req.From,
			msgs:    msgs,
			fromIdx: req.FromIdx,
			txIdx:   0,
		},
	}

	return tx, nil
}

// GenerateTx creates a single MsgCreateSpotLimitOrder per call.
// When ordersPerMarket > 1 or multiple markets are configured, multiple Msg
// messages are packed into the same transaction.
func (p *exchangeSpotLimitOrdersProvider) GenerateTx(req TxRequest) (Tx, error) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	sender := req.From.Key.AccAddress()
	// index=1：非默认子账户，资金在交易所内部 deposit 账本，下单不触碰 bank
	subaccountID := subaccount(req.From.Key.Address(), 1).Hex()

	var msgs []sdk.Msg

	if len(p.spotMarketIDs) == 0 {
		return nil, errors.New("no spot market IDs configured")
	}
	marketID := p.spotMarketIDs[r.Intn(len(p.spotMarketIDs))]
	for orderIdx := 0; orderIdx < p.ordersPerMarket; orderIdx++ {
		// 随机价格：50.001 ~ 60.000
		spotPriceValue := int64(r.Int63n(10000) + 50001)
		spotPrice := math.LegacyNewDecFromIntWithPrec(math.NewInt(spotPriceValue), 3)

		// 随机数量：0.001 ~ 100.000
		quantity := math.LegacyNewDecFromIntWithPrec(math.NewInt(r.Int63n(100000)+1), 3)

		cid := fmt.Sprintf("%d-%s", req.FromIdx, uuid.New().String()[:8])

		var orderType exchangev2types.OrderType
		if r.Intn(2) == 0 {
			orderType = exchangev2types.OrderType_BUY
		} else {
			orderType = exchangev2types.OrderType_SELL
		}

		msg := &exchangev2types.MsgCreateSpotLimitOrder{
			Sender: string(sender),
			Order: exchangev2types.SpotOrder{
				MarketId:  string(marketID),
				OrderType: orderType,
				OrderInfo: exchangev2types.OrderInfo{
					FeeRecipient: string(sender),
					Price:        spotPrice,
					Quantity:     quantity,
					Cid:          cid,
					SubaccountId: subaccountID,
				},
			},
		}

		p.logger.WithFields(log.Fields{
			"order_type": orderType.String(),
			"price":      spotPrice.String(),
			"quantity":   quantity.String(),
			"market_id":  marketID,
			"cid":        cid,
		}).Debug("📝 Creating spot limit order via MsgCreateSpotLimitOrder")

		msgs = append(msgs, msg)
	}

	if len(msgs) == 0 {
		return nil, errors.New("no spot market IDs configured")
	}

	tx := &exchangeSpotLimitOrderTx{
		baseTx: baseTx{
			from:    req.From,
			msgs:    msgs,
			fromIdx: req.FromIdx,
			txIdx:   req.TxIdx,
		},
	}

	return tx, nil
}

func (p *exchangeSpotLimitOrdersProvider) BuildAndSignTx(
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
