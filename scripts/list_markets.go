package main

import (
	"context"
	"fmt"
	"os"
	"time"

	exchangev2types "github.com/InjectiveLabs/sdk-go/chain/exchange/types/v2"
	"github.com/InjectiveLabs/sdk-go/client/common"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	network := common.LoadNetwork("local", "")
	network.ChainId = "stressbiya-801"
	network.TmEndpoint = "http://127.0.0.1:26657"
	network.ChainGrpcEndpoint = "127.0.0.1:9900"

	// 创建 gRPC 连接
	grpcConn, err := grpc.NewClient(network.ChainGrpcEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: 无法连接到 gRPC 端点: %v\n", err)
		os.Exit(1)
	}
	defer grpcConn.Close()

	// 创建 exchange query client
	exchangeClient := exchangev2types.NewQueryClient(grpcConn)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 查询所有现货市场
	resp, err := exchangeClient.SpotMarkets(ctx, &exchangev2types.QuerySpotMarketsRequest{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: 无法查询市场: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("")
	fmt.Println("========== 市场列表 ==========")

	if resp == nil || len(resp.Markets) == 0 {
		fmt.Println("(无市场)")
		fmt.Println("")
		fmt.Println("市场总数: 0")
		return
	}

	// 打印市场ID列表
	for i, market := range resp.Markets {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Print(market.MarketId)
	}
	fmt.Println("")
	fmt.Println("")
	fmt.Printf("市场总数: %d\n", len(resp.Markets))

	// 打印详细信息
	if len(resp.Markets) > 0 {
		fmt.Println("")
		fmt.Println("========== 市场详情 ==========")
		for _, market := range resp.Markets {
			fmt.Printf("Market ID: %s\n", market.MarketId)
			fmt.Printf("  Ticker: %s\n", market.Ticker)
			fmt.Printf("  Base Denom: %s\n", market.BaseDenom)
			fmt.Printf("  Quote Denom: %s\n", market.QuoteDenom)
			fmt.Printf("  Status: %s\n", market.Status)
			fmt.Println("")
		}
	}
}
