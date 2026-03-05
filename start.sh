make install

sh stop.sh

rm -rf chain-stresser-deploy
# template: evm  
# app.evm.toml.tpl | config.prod.toml.tpl | genesis.evm.json.tpl | 
chain-stresser generate --accounts-num 5000 --validators 4 --sentries 0 --instances 1 --prod --native

mkdir -p chain-stresser-deploy/log/

VALIDATOR_DIR="./chain-stresser-deploy/validators"
LOG_DIR="./chain-stresser-deploy/log"

for i in 0 1 2 3; do
    biyachaind --home="$VALIDATOR_DIR/$i" start > "$LOG_DIR/node$i.log" 2>&1 &
    echo $! > "$LOG_DIR/pid$i.pid"
done