package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	exchangev1types "github.com/InjectiveLabs/sdk-go/chain/exchange/types"
	exchangev2types "github.com/InjectiveLabs/sdk-go/chain/exchange/types/v2"
	"github.com/InjectiveLabs/sdk-go/client/common"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "用法: %s <market_id>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "示例: %s 0xb322bce686ec25364be50728812e33741da1d82e9c91c2c89b91b91d26b0e9c5\n", os.Args[0])
		os.Exit(1)
	}

	marketID := os.Args[1]

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

	// 创建 exchange query client（尝试 v1beta1 和 v2）
	exchangeV1Client := exchangev1types.NewQueryClient(grpcConn)
	exchangeV2Client := exchangev2types.NewQueryClient(grpcConn)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 查询订单簿（L3格式，包含买卖盘）
	fmt.Printf("查询市场订单簿: %s\n", marketID)
	fmt.Println("")

	// 先尝试使用 v1beta1 的查询方法
	fmt.Println("尝试使用 v1beta1.Query.L3SpotOrderBook...")
	
	// 尝试不同的 Market ID 格式
	marketIDVariants := []string{
		marketID,                                    // 原始格式（带 0x）
		strings.TrimPrefix(marketID, "0x"),         // 不带 0x
		strings.TrimPrefix(marketID, "0X"),         // 不带 0X（大写）
	}
	
	var v1Resp *exchangev1types.QueryFullSpotOrderbookResponse
	var v1Err error
	
	for i, mid := range marketIDVariants {
		fmt.Printf("尝试 Market ID 格式 %d: %s\n", i+1, mid)
		v1Resp, v1Err = exchangeV1Client.L3SpotOrderBook(ctx, &exchangev1types.QueryFullSpotOrderbookRequest{
			MarketId: mid,
		})
		if v1Err == nil && v1Resp != nil {
			// 检查响应是否为空
			if (v1Resp.Bids != nil && len(v1Resp.Bids) > 0) || (v1Resp.Asks != nil && len(v1Resp.Asks) > 0) {
				fmt.Printf("使用 Market ID 格式 %d 查询成功\n", i+1)
				break
			}
		}
	}
	
	if v1Err == nil && v1Resp != nil {
		fmt.Printf("v1beta1 查询成功\n")
			// 打印调试信息
			respJSON, _ := json.MarshalIndent(v1Resp, "", "  ")
			fmt.Printf("订单簿响应 (原始):\n%s\n\n", respJSON)
			
			// 检查响应结构 - 使用反射查看所有字段
			fmt.Printf("响应类型: %T\n", v1Resp)
			fmt.Printf("Bids 字段: %v (长度: %d)\n", v1Resp.Bids, len(v1Resp.Bids))
			fmt.Printf("Asks 字段: %v (长度: %d)\n", v1Resp.Asks, len(v1Resp.Asks))
			
			// 检查响应结构
			if v1Resp.Bids != nil && len(v1Resp.Bids) > 0 {
				fmt.Printf("找到 %d 档买单\n", len(v1Resp.Bids))
			} else {
				fmt.Printf("Bids 为空或 nil\n")
			}
			if v1Resp.Asks != nil && len(v1Resp.Asks) > 0 {
				fmt.Printf("找到 %d 档卖单\n", len(v1Resp.Asks))
			} else {
				fmt.Printf("Asks 为空或 nil\n")
			}
		
		fmt.Println("========== 买单 (Bids) ==========")
		if v1Resp.Bids == nil || len(v1Resp.Bids) == 0 {
			fmt.Println("(无买单)")
		} else {
			fmt.Printf("%-30s %-30s\n", "价格", "数量")
			for i, bid := range v1Resp.Bids {
				if bid == nil {
					continue
				}
				fmt.Printf("%-30s %-30s\n", bid.Price, bid.Quantity)
				if i >= 9 { // 只显示前10档
					fmt.Printf("... (还有 %d 档买单)\n", len(v1Resp.Bids)-10)
					break
				}
			}
			fmt.Printf("\n买单总数: %d 档\n", len(v1Resp.Bids))
		}

		fmt.Println("")
		fmt.Println("========== 卖单 (Asks) ==========")
		if v1Resp.Asks == nil || len(v1Resp.Asks) == 0 {
			fmt.Println("(无卖单)")
		} else {
			fmt.Printf("%-30s %-30s\n", "价格", "数量")
			for i, ask := range v1Resp.Asks {
				if ask == nil {
					continue
				}
				fmt.Printf("%-30s %-30s\n", ask.Price, ask.Quantity)
				if i >= 9 { // 只显示前10档
					fmt.Printf("... (还有 %d 档卖单)\n", len(v1Resp.Asks)-10)
					break
				}
			}
			fmt.Printf("\n卖单总数: %d 档\n", len(v1Resp.Asks))
		}

		fmt.Println("")
		fmt.Printf("订单簿总计: 买单 %d 档, 卖单 %d 档\n", len(v1Resp.Bids), len(v1Resp.Asks))
		return
	}

	fmt.Printf("v1beta1 查询失败: %v\n", v1Err)
	
	// 尝试使用 v2 的查询方法
	fmt.Println("尝试使用 v2.Query.L3SpotOrderBook...")
	v2Resp, v2Err := exchangeV2Client.L3SpotOrderBook(ctx, &exchangev2types.QueryFullSpotOrderbookRequest{
		MarketId: marketID,
	})
	if v2Err == nil && v2Resp != nil {
		fmt.Printf("v2 查询成功\n")
		// 打印调试信息
		respJSON, _ := json.MarshalIndent(v2Resp, "", "  ")
		fmt.Printf("订单簿响应:\n%s\n\n", respJSON)
		
		fmt.Println("========== 买单 (Bids) ==========")
		if len(v2Resp.Bids) == 0 {
			fmt.Println("(无买单)")
		} else {
			fmt.Printf("%-30s %-30s\n", "价格", "数量")
			for i, bid := range v2Resp.Bids {
				fmt.Printf("%-30s %-30s\n", bid.Price, bid.Quantity)
				if i >= 9 {
					fmt.Printf("... (还有 %d 档买单)\n", len(v2Resp.Bids)-10)
					break
				}
			}
			fmt.Printf("\n买单总数: %d 档\n", len(v2Resp.Bids))
		}

		fmt.Println("")
		fmt.Println("========== 卖单 (Asks) ==========")
		if len(v2Resp.Asks) == 0 {
			fmt.Println("(无卖单)")
		} else {
			fmt.Printf("%-30s %-30s\n", "价格", "数量")
			for i, ask := range v2Resp.Asks {
				fmt.Printf("%-30s %-30s\n", ask.Price, ask.Quantity)
				if i >= 9 {
					fmt.Printf("... (还有 %d 档卖单)\n", len(v2Resp.Asks)-10)
					break
				}
			}
			fmt.Printf("\n卖单总数: %d 档\n", len(v2Resp.Asks))
		}

		fmt.Println("")
		fmt.Printf("订单簿总计: 买单 %d 档, 卖单 %d 档\n", len(v2Resp.Bids), len(v2Resp.Asks))
		return
	}

	fmt.Fprintf(os.Stderr, "v2 查询也失败: %v\n", v2Err)
	
	// 尝试使用 SpotOrderbook（L2）
	fmt.Println("尝试使用 SpotOrderbook (L2)...")
	spotResp, spotErr := exchangeV1Client.SpotOrderbook(ctx, &exchangev1types.QuerySpotOrderbookRequest{
		MarketId: marketID,
	})
	if spotErr == nil {
		spotRespJSON, _ := json.MarshalIndent(spotResp, "", "  ")
		fmt.Printf("SpotOrderbook 响应:\n%s\n", spotRespJSON)
	} else {
		fmt.Fprintf(os.Stderr, "SpotOrderbook 查询失败: %v\n", spotErr)
		os.Exit(1)
	}
}
