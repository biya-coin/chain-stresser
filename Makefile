all:

lint:
	golangci-lint run ./...

install:
	go install ./cmd/...

# TODO: 用于生成节点配置时，把私钥转成account.json，可以优化掉
build-gen-accounts:
	go build -o bin/gen-accounts ./cmd/gen-accounts/

solidity:
	solc --optimize --optimize-runs 2000000 --combined-json abi,bin eth/solidity/Counter.sol > eth/solidity/Counter.json
	abigen --combined-json eth/solidity/Counter.json --pkg contract --type Counter --out eth/solidity/Counter/Counter.go
	rm eth/solidity/Counter.json

	solc --optimize --optimize-runs 2000000 --combined-json abi,bin eth/solidity/BenchmarkInternalCall.sol > eth/solidity/BenchmarkInternalCall.json
	abigen --combined-json eth/solidity/BenchmarkInternalCall.json --pkg contract --type BenchmarkInternalCall --out eth/solidity/BenchmarkInternalCall/BenchmarkInternalCall.go
	rm eth/solidity/BenchmarkInternalCall.json

gen-0:
	chain-stresser generate --accounts-num 1000 --validators 1 --sentries 0 --instances 1 --evm true

gen-4:
	chain-stresser generate --accounts-num 1000 --validators 4 --sentries 0 --instances 1 --evm true

gen-4-2:
	chain-stresser generate --accounts-num 1000 --validators 4 --sentries 2 --instances 1 --evm true

val-0-start:
	biyachaind --home="./chain-stresser-deploy/validators/0" start

val-0-clean:
	biyachaind --home="./chain-stresser-deploy/validators/0" tendermint unsafe-reset-all

val-1-start:
	biyachaind --home="./chain-stresser-deploy/validators/1" start

val-1-clean:
	biyachaind --home="./chain-stresser-deploy/validators/1" tendermint unsafe-reset-all

val-2-start:
	biyachaind --home="./chain-stresser-deploy/validators/2" start

val-2-clean:
	biyachaind --home="./chain-stresser-deploy/validators/2" tendermint unsafe-reset-all

val-3-start:
	biyachaind --home="./chain-stresser-deploy/validators/3" start

val-3-clean:
	biyachaind --home="./chain-stresser-deploy/validators/3" tendermint unsafe-reset-all

gen-4-native:
	chain-stresser generate --accounts-num 1000 --validators 4 --sentries 0 --instances 1 --evm true --native

compose-up:
	docker compose -f chain-stresser-deploy/docker-compose.yml up -d

compose-down:
	docker compose -f chain-stresser-deploy/docker-compose.yml down

run-bank-send:
	chain-stresser tx-bank-send --accounts ./chain-stresser-deploy/instances/0/accounts.json --accounts-num 1000 --await=false

run-bank-send-many:
	chain-stresser tx-bank-send-many --accounts ./chain-stresser-deploy/instances/0/accounts.json --accounts-num 100 --transactions 10 --targets 100 --await=false --rate-tps 40

run-eth-send:
	chain-stresser tx-eth-send --accounts ./chain-stresser-deploy/instances/0/accounts.json --accounts-num 1000

run-eth-call:
	chain-stresser tx-eth-call --accounts ./chain-stresser-deploy/instances/0/accounts.json --accounts-num 1000

run-eth-deploy:
	chain-stresser tx-eth-deploy --accounts ./chain-stresser-deploy/instances/0/accounts.json --accounts-num 1000

run-eth-internal-call:
	chain-stresser tx-eth-internal-call --accounts ./chain-stresser-deploy/instances/0/accounts.json --accounts-num 1000 --iterations 10000

run-eth-userop:
	chain-stresser tx-eth-userop --accounts ./chain-stresser-deploy/instances/0/accounts.json --accounts-num 10

run-exchange-batch-orders:
	chain-stresser tx-exchange-batch-orders --accounts ./chain-stresser-deploy/instances/0/accounts.json \
	--accounts-num 1000 \
	--spot-market-ids 0xb322bce686ec25364be50728812e33741da1d82e9c91c2c89b91b91d26b0e9c5 \
	--orders-per-market 1 \
	--node-addr 127.0.0.1:26857 \
	--grpc-addr 127.0.0.1:10100 \
	--await=false \
	--transactions 10 \
	--verbose \
	--rate-tps 100

run-exchange-market-orders:
	chain-stresser tx-exchange-market-orders --accounts ./chain-stresser-deploy/instances/0/accounts.json \
	--accounts-num 1000 \
	--spot-market-ids 0xb322bce686ec25364be50728812e33741da1d82e9c91c2c89b91b91d26b0e9c5 \
	--node-addr 127.0.0.1:26857 \
	--grpc-addr 127.0.0.1:10100 \
	--await=false \
	--transactions 50 \
	--rate-tps 300

run-exchange-spot-limit-orders:
	chain-stresser tx-exchange-spot-limit-orders --accounts ./chain-stresser-deploy/instances/0/accounts.json \
	--accounts-num 1000 \
	--spot-market-ids 0xb322bce686ec25364be50728812e33741da1d82e9c91c2c89b91b91d26b0e9c5 \
	--node-addr 127.0.0.1:26657 \
	--grpc-addr 127.0.0.1:9900 \
	--await=false \
	--transactions 50 \
	--rate-tps 200

# 四节点并发压测：同时向 instance 0-3 发限价单，全部完成后退出
SPOT_MARKET_ID      ?= 0xb322bce686ec25364be50728812e33741da1d82e9c91c2c89b91b91d26b0e9c5
STRESS_ACCOUNTS_NUM ?= 1000   
STRESS_TRANSACTIONS ?= 10
STRESS_RATE_TPS     ?= 60
run-exchange-spot-limit-orders-4:
	chain-stresser tx-exchange-spot-limit-orders --accounts ./chain-stresser-deploy/instances/0/accounts.json \
		--accounts-num $(STRESS_ACCOUNTS_NUM) \
		--spot-market-ids $(SPOT_MARKET_ID) \
		--node-addr 127.0.0.1:26657 --grpc-addr 127.0.0.1:9900 \
		--await=false --transactions $(STRESS_TRANSACTIONS) --rate-tps $(STRESS_RATE_TPS) & \
	chain-stresser tx-exchange-spot-limit-orders --accounts ./chain-stresser-deploy/instances/1/accounts.json \
		--accounts-num $(STRESS_ACCOUNTS_NUM) \
		--spot-market-ids $(SPOT_MARKET_ID) \
		--node-addr 127.0.0.1:26757 --grpc-addr 127.0.0.1:10000 \
		--await=false --transactions $(STRESS_TRANSACTIONS) --rate-tps $(STRESS_RATE_TPS) & \
	chain-stresser tx-exchange-spot-limit-orders --accounts ./chain-stresser-deploy/instances/2/accounts.json \
		--accounts-num $(STRESS_ACCOUNTS_NUM) \
		--spot-market-ids $(SPOT_MARKET_ID) \
		--node-addr 127.0.0.1:26857 --grpc-addr 127.0.0.1:10100 \
		--await=false --transactions $(STRESS_TRANSACTIONS) --rate-tps $(STRESS_RATE_TPS) & \
	chain-stresser tx-exchange-spot-limit-orders --accounts ./chain-stresser-deploy/instances/3/accounts.json \
		--accounts-num $(STRESS_ACCOUNTS_NUM) \
		--spot-market-ids $(SPOT_MARKET_ID) \
		--node-addr 127.0.0.1:26957 --grpc-addr 127.0.0.1:10200 \
		--await=false --transactions $(STRESS_TRANSACTIONS) --rate-tps $(STRESS_RATE_TPS) & \
	wait

# monad 限价单：
# 	1. 修改account.json的路径
#   2. 修改端口为26657和19900
run-exchange-spot-limit-orders-monad:
	chain-stresser tx-exchange-spot-limit-orders --accounts /home/cyyu/monad-bft/.monad/instances/0/accounts.json \
	--accounts-num 1000 \
	--spot-market-ids 0xb322bce686ec25364be50728812e33741da1d82e9c91c2c89b91b91d26b0e9c5 \
	--node-addr 127.0.0.1:26657 \
	--grpc-addr 127.0.0.1:19900 \
	--chain-id biyachain-1 \
	--await=false \
	--transactions 50 \
	--rate-tps 300

run-wasm-store-code:
	chain-stresser tx-wasm-store-code --accounts ./chain-stresser-deploy/instances/0/accounts.json --accounts-num 1000

run-wasm-init-contract:
	chain-stresser tx-wasm-init-contract --accounts ./chain-stresser-deploy/instances/0/accounts.json --accounts-num 1000

run-wasm-exec-contract:
	chain-stresser tx-wasm-exec-contract --accounts ./chain-stresser-deploy/instances/0/accounts.json --accounts-num 1000

args = $(foreach a,$($(subst _,-,$1)_args),$(if $(value $a),"$($a)"))
eth-counter-get_args = contract

eth-counter-get:
	etherman -N Counter -S ./eth/solidity/Counter.sol call $(call args,$@) getCount

eth-counter-deploy:
	etherman -N Counter -S ./eth/solidity/Counter.sol -P 58aeee3e3848e52689b9edca5fccba193c755b02686e6fc34fd13596e5521ebb deploy 0x00

eth-erc20-setup:
	@if [ -z "$(STAKER_KEY)" ]; then \
		chain-stresser deploy-erc20 --mint --mint-amount=$(AMOUNT_PER_ACCOUNT); \
	else \
		chain-stresser deploy-erc20 --staker-key=$(STAKER_KEY) --mint --mint-amount=$(AMOUNT_PER_ACCOUNT); \
	fi

eth-erc20-userop-stress:
	chain-stresser tx-eth-erc20-userop \
		--accounts chain-stresser-deploy/instances/0/accounts.json \
		--accounts-num 100 \
		--transactions 10 \
		--recipient-address 0x0000000000000000000000000000000000000001 \
		--await=false

create-markets:
	go run scripts/create-markets/create_markets.go

list-markets:
	go run scripts/list-markets/list_markets.go

list-orderbook:
	@if [ -z "$(MARKET_ID)" ]; then \
		echo "用法: make list-orderbook MARKET_ID=0x..."; \
		echo "示例: make list-orderbook MARKET_ID=0xb322bce686ec25364be50728812e33741da1d82e9c91c2c89b91b91d26b0e9c5"; \
		exit 1; \
	fi
	go run scripts/list-orderbook/list_orderbook.go $(MARKET_ID)

cook:
	rsync -r ../chain-stresser cooking:~/go/src/

.PHONY: lint install solidity cook
.PHONY: gen-0 val-0-start val-0-clean
.PHONY: run-bank-send run-eth-send run-eth-call
.PHONY: run-exchange-batch-orders run-exchange-market-orders run-exchange-spot-limit-orders run-exchange-spot-limit-orders-4node
.PHONY: eth-counter-get eth-erc20-setup eth-erc20-userop-stress
