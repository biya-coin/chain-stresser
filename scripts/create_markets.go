package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdktypes "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types/v1beta1"

	exchangev2types "github.com/InjectiveLabs/sdk-go/chain/exchange/types/v2"
	tokenfactorytypes "github.com/InjectiveLabs/sdk-go/chain/tokenfactory/types"
	chainclient "github.com/InjectiveLabs/sdk-go/client/chain"
	"github.com/InjectiveLabs/sdk-go/client/common"
)

const (
	batchCount              = 1  // 创建500个代币和市场
	defaultDecimals         = 18 // 默认代币精度
	batchSize               = 50 // 每批处理的代币数量（从创建到投票的完整流程）
	marketProposalBatchSize = 50 // 每批提交的市场提案数量（避免超过 gas limit）
)

type TokenInfo struct {
	Index    int
	Subdenom string
	Symbol   string
	Denom    string
	Decimals uint32
}

// getProjectRoot 获取项目根目录
func getProjectRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// 如果当前在 scripts 目录，返回上一级
	if filepath.Base(wd) == "scripts" {
		return filepath.Dir(wd), nil
	}

	return wd, nil
}

func main() {
	network := common.LoadNetwork("local", "")
	network.ChainId = "stressbiya-801" // 从 genesis.json 中读取的实际 Chain ID
	network.TmEndpoint = "http://127.0.0.1:26657"
	network.ChainGrpcEndpoint = "127.0.0.1:9900"
	tmClient, err := rpchttp.New(network.TmEndpoint)
	if err != nil {
		panic(err)
	}

	// 获取项目根目录
	projectRoot, err := getProjectRoot()
	if err != nil {
		panic(fmt.Errorf("获取项目根目录失败: %v", err))
	}

	// 使用项目相对路径 - 从keyring-test目录读取验证者账户
	// InitCosmosKeyring 的第一个参数应该是父目录，BackendTest 会在其中查找 keyring-test 子目录
	validatorHome := filepath.Join(projectRoot, "chain-stresser-deploy", "validators", "0")
	keyringTestDir := filepath.Join(validatorHome, "keyring-test")
	fmt.Printf("使用验证者目录: %s\n", validatorHome)
	fmt.Printf("keyring-test目录: %s\n", keyringTestDir)

	// 检查keyring目录是否存在
	if _, err := os.Stat(keyringTestDir); err != nil {
		panic(fmt.Errorf("keyring目录不存在: %s，请先运行 chain-stresser generate 生成配置", keyringTestDir))
	}

	// 初始化第一个账号（主账号，用于创建代币和市场）- 从keyring-test读取
	// InitCosmosKeyring 的第一个参数应该是父目录，BackendTest 会在其中查找 keyring-test 子目录
	fmt.Println("正在从keyring-test初始化主账户（验证者账户）...")
	senderAddress, cosmosKeyring, err := chainclient.InitCosmosKeyring(
		validatorHome, // 使用父目录，BackendTest 会在其中查找 keyring-test 子目录
		"biyachaind",
		"test",
		"validator",
		"",
		"",   // 空字符串表示从keyring读取，不提供私钥
		true, // true = 使用keyring中已存在的密钥
	)
	if err != nil {
		panic(fmt.Errorf("从keyring初始化主账户失败: %v", err))
	}
	fmt.Printf("✓ 主账户（验证者账户）初始化成功: %s\n", senderAddress.String())

	clientCtx, err := chainclient.NewClientContext(
		network.ChainId,
		senderAddress.String(),
		cosmosKeyring,
	)
	if err != nil {
		panic(err)
	}
	clientCtx = clientCtx.WithNodeURI(network.TmEndpoint).WithClient(tmClient)

	// 创建 chainClient
	// 注意：如果账户在链上不存在，NewChainClientV2 会失败
	// 这种情况下，账户需要先在 genesis.json 中创建，或者先发送一笔交易创建账户
	chainClient, err := chainclient.NewChainClientV2(
		clientCtx,
		network,
		common.OptionGasPrices("500000000byb"), // 设置 gas price 和 fee denom 为 byb
	)
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "key not found") {
			fmt.Printf("⚠️  账户在链上不存在: %s\n", senderAddress.String())
			fmt.Printf("提示: 账户在 genesis.json 中，但链上不存在。\n")
			fmt.Printf("可能原因:\n")
			fmt.Printf("  1. 链重启了，但使用了不同的 genesis.json\n")
			fmt.Printf("  2. 链的状态和 genesis.json 不匹配\n")
			fmt.Printf("\n解决方案:\n")
			fmt.Printf("  1. 重启链并确保使用正确的 genesis.json\n")
			fmt.Printf("  2. 或者先发送一笔交易创建账户\n")
			fmt.Printf("  3. 或者使用 validator 账户（应该在链上存在）\n")
			panic(fmt.Errorf("账户在链上不存在: %s", senderAddress.String()))
		}
		panic(fmt.Errorf("创建 chainClient 失败: %v", err))
	}

	// 初始化第二个账号（用于投票）- 使用同一个验证者账户
	fmt.Println("正在从keyring-test初始化投票账户（验证者账户）...")
	voterAddress, voterKeyring, err := chainclient.InitCosmosKeyring(
		validatorHome,
		"biyachaind",
		"test",
		"validator", // 使用同一个验证者账户
		"",
		"",   // 空字符串表示从keyring读取
		true, // true = 使用keyring中已存在的密钥
	)
	if err != nil {
		panic(fmt.Errorf("从keyring初始化投票账户失败: %v", err))
	}
	fmt.Printf("✓ 投票账户（验证者账户）初始化成功: %s\n", voterAddress.String())

	voterClientCtx, err := chainclient.NewClientContext(
		network.ChainId,
		voterAddress.String(),
		voterKeyring,
	)
	if err != nil {
		panic(err)
	}
	voterClientCtx = voterClientCtx.WithNodeURI(network.TmEndpoint).WithClient(tmClient)

	voterChainClient, err := chainclient.NewChainClientV2(
		voterClientCtx,
		network,
		common.OptionGasPrices("500000000byb"), // 设置 gas price 和 fee denom 为 byb
	)
	if err != nil {
		panic(err)
	}

	fmt.Printf("主账号地址: %s\n", senderAddress.String())
	fmt.Printf("投票账号地址: %s\n", voterAddress.String())

	ctx, cancel := context.WithTimeout(context.Background(), 1000*time.Second)
	defer cancel()

	fmt.Printf("钱包地址: %s 将创建 %d 个代币和市场（每批 %d 个）\n", senderAddress.String(), batchCount, batchSize)

	// 分批处理
	totalMarketsCreated := 0
	proposalIDStart := uint64(1)
	for batchStart := 1; batchStart <= batchCount; batchStart += batchSize {
		batchEnd := batchStart + batchSize - 1
		if batchEnd > batchCount {
			batchEnd = batchCount
		}
		batchNum := (batchStart-1)/batchSize + 1
		totalBatches := (batchCount + batchSize - 1) / batchSize

		fmt.Printf("\n========== 批次 %d/%d: 处理代币 %d-%d ==========\n", batchNum, totalBatches, batchStart, batchEnd)

		fmt.Printf("\n[批次 %d] === 阶段1: 批量创建代币 ===\n", batchNum)
		tokenInfos := createTokensBatch(ctx, chainClient, senderAddress, batchStart, batchEnd)

		if len(tokenInfos) == 0 {
			fmt.Printf("[批次 %d] 跳过：没有需要创建的代币\n", batchNum)
			continue
		}

		fmt.Printf("\n[批次 %d] === 阶段2: 批量设置元数据 ===\n", batchNum)
		tokensWithMetadata := setMetadataBatch(ctx, chainClient, senderAddress, tokenInfos, false)

		fmt.Printf("\n[批次 %d] === 阶段2.5: 批量铸造代币 ===\n", batchNum)
		tokensMinted := mintTokensBatch(ctx, chainClient, senderAddress, tokensWithMetadata)

		fmt.Printf("\n[批次 %d] === 阶段3: 批量创建市场并投票 ===\n", batchNum)
		marketsCreated, proposalsCreated := createMarketsBatch(ctx, chainClient, senderAddress, voterChainClient, voterAddress, tokensMinted, proposalIDStart)
		totalMarketsCreated += len(marketsCreated)
		fmt.Printf("[批次 %d] 完成: 已创建 %d 个市场\n", batchNum, len(marketsCreated))

		proposalIDStart += uint64(proposalsCreated)

		if batchEnd < batchCount {
			time.Sleep(2 * time.Second)
		}
	}

	fmt.Printf("\n========== 全部完成 ==========\n")
	fmt.Printf("总共创建了 %d 个市场\n", totalMarketsCreated)
}

// 阶段1: 批量创建代币
func createTokensBatch(ctx context.Context, chainClient chainclient.ChainClientV2, senderAddress sdktypes.AccAddress, startIndex, endIndex int) []TokenInfo {
	existingDenoms := make(map[string]bool)
	denomsResp, err := chainClient.FetchDenomsFromCreator(ctx, senderAddress.String())
	if err == nil && denomsResp != nil {
		for _, denom := range denomsResp.Denoms {
			existingDenoms[denom] = true
		}
		fmt.Printf("已找到 %d 个已存在的代币\n", len(existingDenoms))
	}

	var msgsToCreate []sdktypes.Msg
	var tokenInfosToCreate []TokenInfo

	for i := startIndex; i <= endIndex; i++ {
		subdenom := fmt.Sprintf("token%d", i)
		symbol := fmt.Sprintf("TEST%d", i)
		denom := fmt.Sprintf("factory/%s/%s", senderAddress.String(), subdenom)

		// 创建 TokenInfo（无论代币是否存在都需要）
		tokenInfo := TokenInfo{
			Index:    i,
			Subdenom: subdenom,
			Symbol:   symbol,
			Denom:    denom,
			Decimals: defaultDecimals,
		}

		if existingDenoms[denom] {
			log.Printf("跳过 %d/%d: 代币已存在: %s，将继续创建市场\n", i, endIndex, denom)
			// 即使代币已存在，也添加到 tokenInfosToCreate，以便后续创建市场
			tokenInfosToCreate = append(tokenInfosToCreate, tokenInfo)
			continue
		}

		msg := &tokenfactorytypes.MsgCreateDenom{
			Sender:         senderAddress.String(),
			Subdenom:       subdenom,
			Name:           fmt.Sprintf("Test Token %d", i),
			Symbol:         symbol,
			Decimals:       defaultDecimals,
			AllowAdminBurn: true,
		}

		msgsToCreate = append(msgsToCreate, msg)
		tokenInfosToCreate = append(tokenInfosToCreate, tokenInfo)
	}

	if len(msgsToCreate) == 0 {
		fmt.Println("所有代币都已存在，无需创建")
		// 即使代币已存在，也返回代币信息以便继续创建市场
		return tokenInfosToCreate
	}

	fmt.Printf("准备批量创建 %d 个代币...\n", len(msgsToCreate))

	pollInterval := 1 * time.Second
	maxRetries := uint32(30)
	response, err := chainClient.SyncBroadcastMsg(ctx, &pollInterval, maxRetries, msgsToCreate...)
	if err != nil {
		log.Fatalf("批量创建代币失败: %v\n", err)
	}

	if response.TxResponse.Code != 0 {
		log.Fatalf("批量创建代币失败: code=%d, log=%s\n", response.TxResponse.Code, response.TxResponse.RawLog)
	}

	fmt.Printf("✅ 批量创建代币成功 (tx: %s, height: %d)\n", response.TxResponse.TxHash, response.TxResponse.Height)

	return tokenInfosToCreate
}

// 阶段2: 批量设置元数据
func setMetadataBatch(ctx context.Context, chainClient chainclient.ChainClientV2, senderAddress sdktypes.AccAddress, tokenInfos []TokenInfo, queryAll bool) []TokenInfo {
	allTokenInfos := make(map[string]TokenInfo)

	for _, tokenInfo := range tokenInfos {
		allTokenInfos[tokenInfo.Denom] = tokenInfo
	}

	if queryAll {
		fmt.Println("查询所有已存在的代币...")
		denomsResp, err := chainClient.FetchDenomsFromCreator(ctx, senderAddress.String())
		if err == nil && denomsResp != nil {
			for _, denom := range denomsResp.Denoms {
				if _, exists := allTokenInfos[denom]; !exists {
					parts := strings.Split(denom, "/")
					if len(parts) == 3 && parts[0] == "factory" {
						subdenom := parts[2]
						symbol := strings.ToUpper(subdenom)
						allTokenInfos[denom] = TokenInfo{
							Subdenom: subdenom,
							Symbol:   symbol,
							Denom:    denom,
							Decimals: defaultDecimals,
						}
					}
				}
			}
		}
	}

	var msgsToSet []sdktypes.Msg
	var tokenInfosToSet []TokenInfo

	for _, tokenInfo := range allTokenInfos {
		microDenomUnit := banktypes.DenomUnit{
			Denom:    tokenInfo.Denom,
			Exponent: 0,
			Aliases:  []string{fmt.Sprintf("micro%s", tokenInfo.Subdenom)},
		}
		denomUnit := banktypes.DenomUnit{
			Denom:    tokenInfo.Symbol,
			Exponent: tokenInfo.Decimals,
			Aliases:  []string{tokenInfo.Symbol},
		}

		metadata := banktypes.Metadata{
			Description: "Test token created for stress testing",
			DenomUnits:  []*banktypes.DenomUnit{&microDenomUnit, &denomUnit},
			Base:        tokenInfo.Denom,
			Display:     tokenInfo.Symbol,
			Name:        fmt.Sprintf("Test Token %s", tokenInfo.Symbol),
			Symbol:      tokenInfo.Symbol,
			Decimals:    tokenInfo.Decimals,
		}

		msg := &tokenfactorytypes.MsgSetDenomMetadata{
			Sender:   senderAddress.String(),
			Metadata: metadata,
		}

		msgsToSet = append(msgsToSet, msg)
		tokenInfosToSet = append(tokenInfosToSet, tokenInfo)
	}

	if len(msgsToSet) == 0 {
		fmt.Println("所有代币的元数据都已存在")
		var allTokens []TokenInfo
		for _, tokenInfo := range allTokenInfos {
			allTokens = append(allTokens, tokenInfo)
		}
		return allTokens
	}

	fmt.Printf("准备批量设置 %d 个代币的元数据...\n", len(msgsToSet))

	pollInterval := 1 * time.Second
	maxRetries := uint32(30)
	response, err := chainClient.SyncBroadcastMsg(ctx, &pollInterval, maxRetries, msgsToSet...)
	if err != nil {
		log.Fatalf("批量设置元数据失败: %v\n", err)
	}

	if response.TxResponse.Code != 0 {
		log.Fatalf("批量设置元数据失败: code=%d, log=%s\n", response.TxResponse.Code, response.TxResponse.RawLog)
	}

	fmt.Printf("✅ 批量设置元数据成功 (tx: %s, height: %d)\n", response.TxResponse.TxHash, response.TxResponse.Height)

	return tokenInfosToSet
}

// 阶段2.5: 批量铸造代币
func mintTokensBatch(ctx context.Context, chainClient chainclient.ChainClientV2, senderAddress sdktypes.AccAddress, tokenInfos []TokenInfo) []TokenInfo {
	var msgsToMint []sdktypes.Msg
	var tokensToMint []TokenInfo

	for _, tokenInfo := range tokenInfos {
		supplyResp, err := chainClient.GetBankSupplyOf(ctx, tokenInfo.Denom)
		if err == nil && supplyResp != nil && !supplyResp.Amount.IsZero() {
			continue
		}

		mintAmountInt := math.NewInt(1_000_000)
		for i := uint32(0); i < tokenInfo.Decimals; i++ {
			mintAmountInt = mintAmountInt.Mul(math.NewInt(10))
		}

		msg := &tokenfactorytypes.MsgMint{
			Sender:   senderAddress.String(),
			Amount:   sdktypes.Coin{Denom: tokenInfo.Denom, Amount: mintAmountInt},
			Receiver: "",
		}

		msgsToMint = append(msgsToMint, msg)
		tokensToMint = append(tokensToMint, tokenInfo)
	}

	if len(msgsToMint) == 0 {
		fmt.Println("所有代币都已铸造")
		return tokenInfos
	}

	fmt.Printf("准备批量铸造 %d 个代币...\n", len(msgsToMint))

	pollInterval := 1 * time.Second
	maxRetries := uint32(30)
	response, err := chainClient.SyncBroadcastMsg(ctx, &pollInterval, maxRetries, msgsToMint...)
	if err != nil {
		log.Fatalf("批量铸造代币失败: %v\n", err)
	}

	if response.TxResponse.Code != 0 {
		log.Fatalf("批量铸造代币失败: code=%d, log=%s\n", response.TxResponse.Code, response.TxResponse.RawLog)
	}

	fmt.Printf("✅ 批量铸造代币成功 (tx: %s, height: %d)\n", response.TxResponse.TxHash, response.TxResponse.Height)

	return tokenInfos
}

// 阶段3: 批量创建市场
func createMarketsBatch(ctx context.Context, chainClient chainclient.ChainClientV2, senderAddress sdktypes.AccAddress, voterChainClient chainclient.ChainClientV2, voterAddress sdktypes.AccAddress, tokenInfos []TokenInfo, proposalIDStart uint64) ([]string, int) {
	var msgs []sdktypes.Msg
	var marketTickers []string

	for _, tokenInfo := range tokenInfos {
		ticker := fmt.Sprintf("%s/BYB", tokenInfo.Symbol)

		supplyValid := false
		for retry := 0; retry < 10; retry++ {
			supplyResp, err := chainClient.GetBankSupplyOf(ctx, tokenInfo.Denom)
			if err == nil && supplyResp != nil && !supplyResp.Amount.IsZero() {
				supplyValid = true
				break
			}
			if retry < 9 {
				time.Sleep(500 * time.Millisecond)
			}
		}
		if !supplyValid {
			log.Printf("  ❌ 代币不在 supply 中，跳过: %s\n", tokenInfo.Denom)
			continue
		}

		makerFeeRate := math.LegacyMustNewDecFromStr("0.001")
		takerFeeRate := math.LegacyMustNewDecFromStr("0.0015")
		proposal := &exchangev2types.SpotMarketLaunchProposal{
			Title:               fmt.Sprintf("List %s", ticker),
			Description:         fmt.Sprintf("Launch %s", ticker),
			Ticker:              ticker,
			BaseDenom:           tokenInfo.Denom,
			QuoteDenom:          "byb", // 使用 byb 作为报价代币（链上的 bond denom）
			MinPriceTickSize:    math.LegacyMustNewDecFromStr("0.00000001"),
			MinQuantityTickSize: math.LegacyMustNewDecFromStr("0.0001"),
			MinNotional:         math.LegacyMustNewDecFromStr("0"),
			MakerFeeRate:        &makerFeeRate,
			TakerFeeRate:        &takerFeeRate,
			BaseDecimals:        tokenInfo.Decimals,
			QuoteDecimals:       18,
		}

		proposalAny, err := codectypes.NewAnyWithValue(proposal)
		if err != nil {
			log.Printf("  ❌ 创建提案失败: %v\n", err)
			continue
		}

		// 使用 byb 作为提案存款代币（治理模块只接受 byb）
		depositAmount := sdktypes.NewCoins(sdktypes.NewCoin("byb", math.NewInt(100000000000000000)))
		msg := &govtypes.MsgSubmitProposal{
			Content:        proposalAny,
			InitialDeposit: depositAmount,
			Proposer:       senderAddress.String(),
		}

		msgs = append(msgs, msg)
		marketTickers = append(marketTickers, ticker)
	}

	if len(msgs) == 0 {
		return []string{}, 0
	}

	fmt.Printf("准备分批提交 %d 个市场提案（每批 %d 个）...\n", len(msgs), marketProposalBatchSize)
	pollInterval := 1 * time.Second
	maxRetries := uint32(10)
	totalProposals := 0
	var proposalIDs []uint64
	var marketsCreated []string

	for i := 0; i < len(msgs); i += marketProposalBatchSize {
		end := i + marketProposalBatchSize
		if end > len(msgs) {
			end = len(msgs)
		}

		batchMsgs := msgs[i:end]
		batchTickers := marketTickers[i:end]
		batchNum := (i / marketProposalBatchSize) + 1

		fmt.Printf("提交提案批次 %d (%d 个提案)...\n", batchNum, len(batchMsgs))
		response, err := chainClient.SyncBroadcastMsg(ctx, &pollInterval, maxRetries, batchMsgs...)
		if err != nil {
			log.Printf("❌ 批次 %d 提交失败: %v\n", batchNum, err)
			continue
		}

		if response.TxResponse.Code != 0 {
			log.Printf("❌ 批次 %d 提交失败: code=%d, log=%s\n", batchNum, response.TxResponse.Code, response.TxResponse.RawLog)
			continue
		}

		fmt.Printf("✅ 批次 %d 提交成功 (tx: %s)\n", batchNum, response.TxResponse.TxHash)

		batchProposalIDs := extractProposalIDs(response.TxResponse.Events)
		proposalIDs = append(proposalIDs, batchProposalIDs...)
		marketsCreated = append(marketsCreated, batchTickers...)
		totalProposals += len(batchMsgs)

		if end < len(msgs) {
			time.Sleep(2 * time.Second)
		}
	}

	if len(proposalIDs) == 0 {
		for i := uint64(0); i < uint64(totalProposals); i++ {
			proposalIDs = append(proposalIDs, proposalIDStart+i)
		}
	}

	// 投票
	fmt.Println("\n等待提案进入投票期...")
	time.Sleep(2 * time.Second)

	fmt.Printf("开始投票（共 %d 个提案）...\n", len(proposalIDs))
	voteInBatches(ctx, chainClient, voterChainClient, senderAddress, voterAddress, proposalIDs)

	// 等待提案通过和执行（投票期 + 执行时间）
	// 根据 genesis.json，voting_period 是 10s（测试环境）
	fmt.Println("\n等待提案通过和执行（投票期约10秒）...")
	time.Sleep(2 * time.Second) // 等待投票期结束 + 执行时间

	fmt.Printf("✅ 提案应该已经通过并执行，市场应该已创建\n")

	return marketsCreated, totalProposals
}

func voteInBatches(ctx context.Context, chainClient chainclient.ChainClientV2, voterChainClient chainclient.ChainClientV2, senderAddress sdktypes.AccAddress, voterAddress sdktypes.AccAddress, proposalIDs []uint64) {
	voteBatchSize := 50
	pollInterval := 1 * time.Second
	maxRetries := uint32(10)

	for i := 0; i < len(proposalIDs); i += voteBatchSize {
		end := i + voteBatchSize
		if end > len(proposalIDs) {
			end = len(proposalIDs)
		}

		var voteMsgs1, voteMsgs2 []sdktypes.Msg
		for _, proposalID := range proposalIDs[i:end] {
			voteMsgs1 = append(voteMsgs1, &govtypes.MsgVote{
				ProposalId: proposalID,
				Voter:      senderAddress.String(),
				Option:     govtypes.OptionYes,
			})
			voteMsgs2 = append(voteMsgs2, &govtypes.MsgVote{
				ProposalId: proposalID,
				Voter:      voterAddress.String(),
				Option:     govtypes.OptionYes,
			})
		}

		batchNum := (i / voteBatchSize) + 1
		fmt.Printf("投票批次 %d (%d 个提案)...\n", batchNum, len(voteMsgs1))

		resp1, _ := chainClient.SyncBroadcastMsg(ctx, &pollInterval, maxRetries, voteMsgs1...)
		if resp1 != nil && resp1.TxResponse.Code == 0 {
			fmt.Printf("  ✅ 账号1投票成功\n")
		}

		resp2, _ := voterChainClient.SyncBroadcastMsg(ctx, &pollInterval, maxRetries, voteMsgs2...)
		if resp2 != nil && resp2.TxResponse.Code == 0 {
			fmt.Printf("  ✅ 账号2投票成功\n")
		}

		if end < len(proposalIDs) {
			time.Sleep(1 * time.Second)
		}
	}
}

func extractProposalIDs(events []abci.Event) []uint64 {
	proposalIDMap := make(map[uint64]bool) // 使用 map 去重
	var proposalIDs []uint64

	for _, event := range events {
		if event.Type == "submit_proposal" || event.Type == "proposal_deposit" {
			for _, attr := range event.Attributes {
				if string(attr.Key) == "proposal_id" {
					proposalID, err := strconv.ParseUint(string(attr.Value), 10, 64)
					if err == nil && !proposalIDMap[proposalID] {
						proposalIDMap[proposalID] = true
						proposalIDs = append(proposalIDs, proposalID)
					}
				}
			}
		}
	}
	return proposalIDs
}
