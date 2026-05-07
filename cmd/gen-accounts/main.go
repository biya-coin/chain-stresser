// gen-accounts: 管理压测账户密钥。
//
// 子命令:
//
//	generate --num N --out DIR
//	    生成 N 个 ethsecp256k1 账户，输出 accounts.json / addresses.json。
//
//	genesis-add --addresses FILE --genesis FILE --balance-byb COINS --balance-usdt COINS
//	    批量将地址列表写入 genesis.json（auth accounts + bank balances + supply），
//	    替代逐条调用 biyachaind genesis add-genesis-account。
package main

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"regexp"
	"sort"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/crypto"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/spf13/cobra"

	crypto_cdc "github.com/InjectiveLabs/sdk-go/chain/crypto/codec"
	"github.com/InjectiveLabs/sdk-go/chain/crypto/ethsecp256k1"
	"github.com/InjectiveLabs/sdk-go/chain/crypto/hd"
	chainsdk "github.com/InjectiveLabs/sdk-go/client/chain"

	"github.com/biya-coin/chain-stresser/v2/chain"
)

func main() {
	rootCmd := &cobra.Command{
		Use:           "gen-accounts",
		SilenceErrors: true,
		SilenceUsage:  false,
	}
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	// ---------- generate ----------
	var (
		genNum        int
		genKeyringDir string
		genOutDir     string
	)
	generateCmd := &cobra.Command{
		Use:   "generate",
		Short: "生成 N 个账户，输出 accounts.json / addresses.json",
		RunE: func(cmd *cobra.Command, args []string) error {
			if genNum <= 0 {
				return fmt.Errorf("--num 必须为正整数")
			}
			if genOutDir == "" {
				return fmt.Errorf("--out 不能为空")
			}

			if err := os.MkdirAll(genOutDir, 0o755); err != nil {
				return fmt.Errorf("创建输出目录失败: %w", err)
			}

			accounts := make([]chain.Secp256k1PrivateKey, 0, genNum)
			addresses := make([]string, 0, genNum)

			for i := 0; i < genNum; i++ {
				_, privKey := chain.GenerateSecp256k1Key()
				accounts = append(accounts, privKey)
				addresses = append(addresses, privKey.AccAddress())
			}

			if err := writeJSON(genOutDir+"/accounts.json", accounts, 0o600); err != nil {
				return err
			}
			if err := writeJSON(genOutDir+"/addresses.json", addresses, 0o644); err != nil {
				return err
			}

			fmt.Printf("生成 %d 个账户 -> %s\n", genNum, genOutDir)
			return nil
		},
	}
	generateCmd.Flags().IntVar(&genNum, "num", 1000, "生成账户数量")
	generateCmd.Flags().StringVar(&genKeyringDir, "keyring-dir", "", "（已废弃，保留兼容，不再使用）")
	generateCmd.Flags().StringVar(&genOutDir, "out", "", "输出 accounts.json / addresses.json 的目录")
	_ = generateCmd.MarkFlagRequired("out")
	rootCmd.AddCommand(generateCmd)

	// ---------- export ----------
	var (
		expKeyringDir string
		expOutDir     string
	)
	exportCmd := &cobra.Command{
		Use:   "export",
		Short: "从已有 keyring-test 目录读取所有私钥，输出 accounts.json / addresses.json",
		RunE: func(cmd *cobra.Command, args []string) error {
			if expKeyringDir == "" {
				return fmt.Errorf("--keyring-dir 不能为空")
			}
			if expOutDir == "" {
				return fmt.Errorf("--out 不能为空")
			}

			kb := newKeyring(expKeyringDir)
			records, err := kb.List()
			if err != nil {
				return fmt.Errorf("读取 keyring 失败: %w", err)
			}
			if len(records) == 0 {
				return fmt.Errorf("keyring 目录 %s 中没有找到任何密钥", expKeyringDir)
			}

			// 按名称排序保证输出稳定
			sort.Slice(records, func(i, j int) bool {
				return records[i].Name < records[j].Name
			})

			accounts := make([]chain.Secp256k1PrivateKey, 0, len(records))
			addresses := make([]string, 0, len(records))

			for _, rec := range records {
				armored, err := kb.ExportPrivKeyArmor(rec.Name, "test")
				if err != nil {
					return fmt.Errorf("导出 %s 私钥失败: %w", rec.Name, err)
				}
				privKeyIface, _, err := crypto.UnarmorDecryptPrivKey(armored, "test")
				if err != nil {
					return fmt.Errorf("解密 %s 私钥失败: %w", rec.Name, err)
				}
				ethPriv, ok := privKeyIface.(*ethsecp256k1.PrivKey)
				if !ok {
					fmt.Fprintf(os.Stderr, "警告: %s 不是 ethsecp256k1 密钥，跳过\n", rec.Name)
					continue
				}
				privKey := chain.Secp256k1PrivateKey(ethPriv.Bytes())
				accounts = append(accounts, privKey)
				addresses = append(addresses, privKey.AccAddress())
			}

			if err := os.MkdirAll(expOutDir, 0o755); err != nil {
				return fmt.Errorf("创建输出目录失败: %w", err)
			}
			if err := writeJSON(expOutDir+"/accounts.json", accounts, 0o600); err != nil {
				return err
			}
			if err := writeJSON(expOutDir+"/addresses.json", addresses, 0o644); err != nil {
				return err
			}

			fmt.Printf("导出 %d 个账户\n  accounts.json / addresses.json -> %s\n", len(accounts), expOutDir)
			return nil
		},
	}
	exportCmd.Flags().StringVar(&expKeyringDir, "keyring-dir", "", "keyring-test 目录路径")
	exportCmd.Flags().StringVar(&expOutDir, "out", "", "输出 accounts.json / addresses.json 的目录")
	_ = exportCmd.MarkFlagRequired("keyring-dir")
	_ = exportCmd.MarkFlagRequired("out")
	rootCmd.AddCommand(exportCmd)

	// ---------- genesis-add ----------
	var (
		gaAddressesFile string
		gaGenesisFile   string
		gaBalanceBYB    string
		gaBalanceUSDT   string
	)
	genesisAddCmd := &cobra.Command{
		Use:   "genesis-add",
		Short: "批量将 addresses.json 中的地址写入 genesis.json（无需逐条调用 biyachaind）",
		RunE: func(cmd *cobra.Command, args []string) error {
			// 读取地址列表
			addrData, err := os.ReadFile(gaAddressesFile)
			if err != nil {
				return fmt.Errorf("读取 addresses 文件失败: %w", err)
			}
			var addresses []string
			if err := json.Unmarshal(addrData, &addresses); err != nil {
				return fmt.Errorf("解析 addresses JSON 失败: %w", err)
			}

			// 读取 genesis.json
			genesisData, err := os.ReadFile(gaGenesisFile)
			if err != nil {
				return fmt.Errorf("读取 genesis.json 失败: %w", err)
			}
			var genesis map[string]json.RawMessage
			if err := json.Unmarshal(genesisData, &genesis); err != nil {
				return fmt.Errorf("解析 genesis.json 失败: %w", err)
			}

			// 解析 app_state
			var appState map[string]json.RawMessage
			if err := json.Unmarshal(genesis["app_state"], &appState); err != nil {
				return fmt.Errorf("解析 app_state 失败: %w", err)
			}

			// 解析 auth
			var auth map[string]json.RawMessage
			if err := json.Unmarshal(appState["auth"], &auth); err != nil {
				return fmt.Errorf("解析 auth 失败: %w", err)
			}
			var authAccounts []json.RawMessage
			if err := json.Unmarshal(auth["accounts"], &authAccounts); err != nil {
				return fmt.Errorf("解析 auth.accounts 失败: %w", err)
			}

			// 解析 bank
			var bank map[string]json.RawMessage
			if err := json.Unmarshal(appState["bank"], &bank); err != nil {
				return fmt.Errorf("解析 bank 失败: %w", err)
			}
			var bankBalances []json.RawMessage
			if err := json.Unmarshal(bank["balances"], &bankBalances); err != nil {
				return fmt.Errorf("解析 bank.balances 失败: %w", err)
			}
			var supply []map[string]string
			if err := json.Unmarshal(bank["supply"], &supply); err != nil {
				return fmt.Errorf("解析 bank.supply 失败: %w", err)
			}

			// 收集已有地址，避免重复
			existingAddrs := make(map[string]bool, len(bankBalances))
			for _, b := range bankBalances {
				var entry struct {
					Address string `json:"address"`
				}
				_ = json.Unmarshal(b, &entry)
				existingAddrs[entry.Address] = true
			}

			// 解析 coin 字符串 "1000byb" -> {denom, amount}
			parseCoin := func(s string) (map[string]string, error) {
				re := regexp.MustCompile(`^(\d+)(.+)$`)
				m := re.FindStringSubmatch(s)
				if m == nil {
					return nil, fmt.Errorf("无法解析 coin: %q", s)
				}
				return map[string]string{"denom": m[2], "amount": m[1]}, nil
			}

			coinBYB, err := parseCoin(gaBalanceBYB)
			if err != nil {
				return err
			}
			coinUSDT, err := parseCoin(gaBalanceUSDT)
			if err != nil {
				return err
			}
			// 按 denom 字母序排列（cosmos SDK 规范）
			coins := []map[string]string{coinBYB, coinUSDT}
			sort.Slice(coins, func(i, j int) bool { return coins[i]["denom"] < coins[j]["denom"] })

			// 批量追加
			added := 0
			for _, addr := range addresses {
				if existingAddrs[addr] {
					continue
				}
				// auth account — 必须用 EthAccount，否则 account_number 查询异常导致签名失败
				accRaw, _ := json.Marshal(map[string]interface{}{
					"@type": "/injective.types.v1beta1.EthAccount",
					"base_account": map[string]interface{}{
						"address":        addr,
						"pub_key":        nil,
						"account_number": "0",
						"sequence":       "0",
					},
					// EOA 标准 code_hash（keccak256 of empty string）
					"code_hash": "xdJGAYb3IzySfn2y3McDwOUAtlPKgic7e/rYBF2FpHA=",
				})
				authAccounts = append(authAccounts, accRaw)
				// bank balance
				balRaw, _ := json.Marshal(map[string]interface{}{
					"address": addr,
					"coins":   coins,
				})
				bankBalances = append(bankBalances, balRaw)
				added++
			}

// 更新 supply（用 big.Int 避免 int64 溢出，余额最大可达 1e27+）
					supplyMap := make(map[string]*big.Int, len(supply))
					for _, s := range supply {
						v := new(big.Int)
						v.SetString(s["amount"], 10)
						supplyMap[s["denom"]] = v
					}
					addedBig := big.NewInt(int64(added))
					for _, coin := range coins {
						amt := new(big.Int)
						amt.SetString(coin["amount"], 10)
						amt.Mul(amt, addedBig)
						if cur, ok := supplyMap[coin["denom"]]; ok {
							cur.Add(cur, amt)
						} else {
							supplyMap[coin["denom"]] = amt
						}
					}
					newSupply := make([]map[string]string, 0, len(supplyMap))
					for d, a := range supplyMap {
						newSupply = append(newSupply, map[string]string{"denom": d, "amount": a.String()})
			}
			sort.Slice(newSupply, func(i, j int) bool { return newSupply[i]["denom"] < newSupply[j]["denom"] })

			// 回写各层
			auth["accounts"], _ = json.Marshal(authAccounts)
			bank["balances"], _ = json.Marshal(bankBalances)
			bank["supply"], _ = json.Marshal(newSupply)
			appState["auth"], _ = json.Marshal(auth)
			appState["bank"], _ = json.Marshal(bank)
			genesis["app_state"], _ = json.Marshal(appState)

			out, err := json.MarshalIndent(genesis, "", "  ")
			if err != nil {
				return fmt.Errorf("序列化 genesis.json 失败: %w", err)
			}
			if err := os.WriteFile(gaGenesisFile, out, 0o644); err != nil {
				return fmt.Errorf("写入 genesis.json 失败: %w", err)
			}

			fmt.Printf("批量写入 %d 个压测账户到 %s\n", added, gaGenesisFile)
			return nil
		},
	}
	genesisAddCmd.Flags().StringVar(&gaAddressesFile, "addresses", "", "addresses.json 文件路径")
	genesisAddCmd.Flags().StringVar(&gaGenesisFile, "genesis", "", "genesis.json 文件路径")
	genesisAddCmd.Flags().StringVar(&gaBalanceBYB, "balance-byb", "", "BYB 余额，如 1000000000000000000000000000byb")
	genesisAddCmd.Flags().StringVar(&gaBalanceUSDT, "balance-usdt", "", "USDT 余额，如 10000000000000000000peggy0x...")
	_ = genesisAddCmd.MarkFlagRequired("addresses")
	_ = genesisAddCmd.MarkFlagRequired("genesis")
	_ = genesisAddCmd.MarkFlagRequired("balance-byb")
	_ = genesisAddCmd.MarkFlagRequired("balance-usdt")
	rootCmd.AddCommand(genesisAddCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ---------- helpers ----------

var cryptoCdc *codec.ProtoCodec

func init() {
	registry := chainsdk.NewInterfaceRegistry()
	crypto_cdc.RegisterInterfaces(registry)
	cryptoCdc = codec.NewProtoCodec(registry)

	// 设置 bech32 前缀（与 chain/init.go 保持一致）
	// chain 包的 init() 在同进程内已执行，这里无需重复 seal，仅依赖 import 副作用。
	_ = chain.GenerateSecp256k1Key // 触发 chain 包 init
}

func newKeyring(dir string) keyring.Keyring {
	kb, err := keyring.New(
		"biyachaind",
		keyring.BackendTest,
		dir,
		nil,
		cryptoCdc,
		hd.EthSecp256k1Option(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法打开 keyring: %v\n", err)
		os.Exit(1)
	}
	return kb
}

func importToKeyring(kb keyring.Keyring, name string, privKey chain.Secp256k1PrivateKey) error {
	ethPriv := &ethsecp256k1.PrivKey{Key: privKey}
	armored := crypto.EncryptArmorPrivKey(ethPriv, "test", ethPriv.Type())
	return kb.ImportPrivKey(name, armored, "test")
}

func writeJSON(path string, v any, perm os.FileMode) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %s 失败: %w", path, err)
	}
	if err := os.WriteFile(path, data, perm); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", path, err)
	}
	return nil
}
