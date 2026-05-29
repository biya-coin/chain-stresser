package payload

import (
	"fmt"
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

	provider := &exchangeSpotLimitOrdersProvider{
		spotMarketIDs:   spotMarketIDs,
		ordersPerMarket: ordersPerMarket,
		minGasPrice:     parsedMinGasPrice,
		maxGasLimit:     75000000,
	}

	provider.logger = log.WithFields(log.Fields{
		"provider": provider.Name(),
	})

	return provider, nil
}

type exchangeSpotLimitOrderTx struct {
	baseTx
}

func (p *exchangeSpotLimitOrdersProvider) Name() string {
	return "exchange_spot_limit_orders_stress"
}

// GenerateInitialTx returns nil – accounts are assumed to have sufficient balance.
func (p *exchangeSpotLimitOrdersProvider) GenerateInitialTx(req TxRequest) (Tx, error) {
	return nil, nil
}

// GenerateTx creates a single MsgCreateSpotLimitOrder per call.
// When ordersPerMarket > 1 or multiple markets are configured, multiple Msg
// messages are packed into the same transaction.
func (p *exchangeSpotLimitOrdersProvider) GenerateTx(req TxRequest) (Tx, error) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	sender := req.From.Key.AccAddress()
	defaultSubaccountID := subaccount(req.From.Key.Address(), 0).Hex()

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
	minGasPriceAmount := p.minGasPrice.Amount
	maxFeeAmount := minGasPriceAmount.Mul(math.NewIntFromUint64(p.maxGasLimit))

	chainTx := chain.Tx{
		Msgs:     unsignedTx.Msgs(),
		GasLimit: p.maxGasLimit,
		Fee: sdk.NewCoins(sdk.NewCoin(
			p.minGasPrice.Denom,
			maxFeeAmount,
		)),
		Memo: p.memoAttach,
	}

	signedResult, err := client.BuildAndSignTx(unsignedTx.From(), chainTx)
	if err != nil {
		return nil, err
	}

	tx := unsignedTx.WithBytes(client.Encode(signedResult))
	return tx, nil
}
