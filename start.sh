sh stop.sh

rm -rf chain-stresser-deploy
# template: evm  
# app.evm.toml.tpl | config.prod.toml.tpl | genesis.evm.json.tpl | 
NODE_NUM=4
BIYACHIAND="/Users/maxwelldu/github/biya/biyachain-core/bin/biyachaind"

log_level="error"
# OE 现已安全（链端 BeginBlocker 在 OE abort 重执行时失效跨块进程级缓存），可开启。
optimistic_execution_enabled=true

chain-stresser generate --accounts-num 1000 --validators $NODE_NUM --sentries 0 --instances 4 --prod --native

mkdir -p node-log/

VALIDATOR_DIR="./chain-stresser-deploy/validators"
LOG_DIR="./node-log"

for i in $(seq 0 $(($NODE_NUM - 1))); do
    $BIYACHIAND --home="$VALIDATOR_DIR/$i" \
        --optimistic-execution-enabled=$optimistic_execution_enabled \
        --log-level=$log_level \
    start > "$LOG_DIR/node$i.log" 2>&1 &
    echo $! > "$LOG_DIR/pid$i.pid"
done
