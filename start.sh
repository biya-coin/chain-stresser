# 1. Stop and delete old data
sh stop.sh
rm -rf chain-stresser-deploy

# 2. Set node number and log level
NODE_NUM=4
log_level="error"

# 3. Set chain startup path（自动按环境探测，无需手改）
# 优先级：① 环境变量 BIYACHIAND（如 `BIYACHIAND=/x/biyachaind ./start.sh` 临时覆盖）
#        ② 下面候选路径里【实际存在且可执行】的第一个（线上 Linux 路径 / 本地 Mac 路径各放一条）
#        ③ 都没有则回退到 PATH 里的 biyachaind
# 这样：线上跑命中线上路径、本地跑命中本地路径，start.sh 不用每次改。
BIYACHIAND_CANDIDATES="
/home/ubuntu/biyachain/biyachain-core/bin/biyachaind
/Users/maxwelldu/github/biya/biyachain-core/bin/biyachaind
"
if [ -z "$BIYACHIAND" ]; then
    for _cand in $BIYACHIAND_CANDIDATES; do
        if [ -x "$_cand" ]; then BIYACHIAND="$_cand"; break; fi
    done
fi
if [ -z "$BIYACHIAND" ]; then
    BIYACHIAND="$(command -v biyachaind 2>/dev/null)"
fi
if [ -z "$BIYACHIAND" ] || [ ! -x "$BIYACHIAND" ]; then
    echo "ERROR: 找不到 biyachaind 可执行文件。请用 BIYACHIAND=/path/to/biyachaind ./start.sh 指定，或把路径加进 BIYACHIAND_CANDIDATES。" >&2
    exit 1
fi
echo "使用 biyachaind: $BIYACHIAND"

# 4. The default startup parameters
# - Optimistic execution enabled
optimistic_execution_enabled=true
# - CometBFT logging: true -> enable; false -> only errors are logged, others will be ignored, no performance overhead
comet_log_enabled=true
# - SeiDB settings
store_backend="seidb"
seidb_enabled=true
seidb_sc_backend="memiavl"
seidb_ss_enable=false
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
        --comet-log-enabled=$comet_log_enabled \
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
