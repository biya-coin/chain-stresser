sh stop.sh

rm -rf chain-stresser-deploy
# template: evm  
# app.evm.toml.tpl | config.prod.toml.tpl | genesis.evm.json.tpl | 
NODE_NUM=4
chain-stresser generate --accounts-num 5000 --validators $NODE_NUM --sentries 0 --instances 1 --prod --native

mkdir -p node-log/

VALIDATOR_DIR="./chain-stresser-deploy/validators"
LOG_DIR="./node-log"

for i in $(seq 0 $(($NODE_NUM - 1))); do
    biyachaind --home="$VALIDATOR_DIR/$i" start > "$LOG_DIR/node$i.log" 2>&1 &
    echo $! > "$LOG_DIR/pid$i.pid"
done
