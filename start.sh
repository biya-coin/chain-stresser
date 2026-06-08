# 1. Stop and delete old data
sh stop.sh
rm -rf chain-stresser-deploy

# 2. Set node number and log level
NODE_NUM=4
log_level="error"

# 3. Set chain startup path
# - If running on your own computer (e.g. MacOS, windows), you need to modify this path
# - For example, BIYACHIAND="/Users/xiaoming/code/biyachain-core/bin/biyachaind"
BIYACHIAND="/home/ubuntu/biyachain/biyachain-core/bin/biyachaind"

# 4. The default startup parameters
# - Optimistic execution enabled
optimistic_execution_enabled=true
# - SeiDB settings
store_backend="seidb"
seidb_enabled=true
seidb_sc_backend="memiavl"
seidb_ss_enable=true
seidb_ss_backend="pebbledb"
# - Exchange
# None for now

# 5. Generate chain config
chain-stresser generate --accounts-num 1000 --validators $NODE_NUM --sentries 0 --instances 4 --prod --native

# 6. Create log directory
mkdir -p node-log/
LOG_DIR="./node-log"

# 7. Start nodes
VALIDATOR_DIR="./chain-stresser-deploy/validators"
for i in $(seq 0 $(($NODE_NUM - 1))); do
    NODE_HOME="$VALIDATOR_DIR/$i"
    $BIYACHIAND --home="$NODE_HOME" \
        --optimistic-execution-enabled=$optimistic_execution_enabled \
        --log-level=$log_level \
        --store.backend="$store_backend" \
        --seidb.enabled=$seidb_enabled \
        --seidb.home="$NODE_HOME/seidb" \
        --seidb.ss-enable=$seidb_ss_enable \
        --seidb.sc-backend="$seidb_sc_backend" \
        --seidb.ss-backend="$seidb_ss_backend" \
    start > "$LOG_DIR/node$i.log" 2>&1 &
    echo $! > "$LOG_DIR/pid$i.pid"
done
