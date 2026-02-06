#!/bin/bash
set -euo pipefail

# 查询 addresses.json 中所有地址的余额
# 用法: bash export_private_keys.sh [addresses_file]
# 示例: bash export_private_keys.sh ./chain-stresser-deploy/instances/0/addresses.json

ADDRESSES_FILE="${1:-./chain-stresser-deploy/instances/0/addresses.json}"

if [ ! -f "$ADDRESSES_FILE" ]; then
  echo "错误: 文件不存在: $ADDRESSES_FILE" >&2
  exit 1
fi

# 读取地址数量
ADDRESS_COUNT=$(jq 'length' "$ADDRESSES_FILE" 2>/dev/null)

query_balance() {
  for i in $(seq 0 $((ADDRESS_COUNT - 1))); do
    ADDRESS=$(jq -r ".[$i]" "$ADDRESSES_FILE" 2>/dev/null)
    if [ -z "$ADDRESS" ] || [ "$ADDRESS" = "null" ]; then
      continue
    fi
    echo "地址 $((i+1))/$ADDRESS_COUNT: $ADDRESS"
    biyachaind q bank balances "$ADDRESS" --node http://localhost:26657
    echo ""
  done
}

query_balance

# 处理每个地址
for i in $(seq 0 $((ADDRESS_COUNT - 1))); do
  # 从 JSON 数组中提取第 i 个元素（地址字符串）
  ADDRESS=$(jq -r ".[$i]" "$ADDRESSES_FILE" 2>/dev/null)
  
  echo "地址: $ADDRESS"
  biyachaind tx bank send validator "$ADDRESS" 1byb --node http://localhost:26657 --home ./chain-stresser-deploy/validators/0 --keyring-backend test --chain-id stressbiya-801 --yes --fees 1000000000000000000byb
  biyachaind tx bank send validator "$ADDRESS" 1peggy0x3de4027B5b0Bf278Db2D187768AC441e9B356360 --node http://localhost:26657 --home ./chain-stresser-deploy/validators/0 --keyring-backend test --chain-id stressbiya-801 --yes --fees 1000000000000000000byb
done


query_balance