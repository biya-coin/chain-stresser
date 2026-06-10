# This is a TOML config file.
# For more information, see https://github.com/toml-lang/toml

###############################################################################
###                           Base Configuration                            ###
###############################################################################

# The minimum gas prices a validator is willing to accept for processing a
# transaction. A transaction's fees must meet the minimum of any denomination
# specified in this config (e.g. 0.25token1,0.0001token2).
minimum-gas-prices = "{{.MinimumGasPrices}}"

# The maximum gas a query coming over rest/grpc may consume.
# If this is set to zero, the query can consume an unbounded amount of gas.
query-gas-limit = "0"

# default: the last 362880 states are kept, pruning at 10 block intervals
# nothing: all historic states will be saved, nothing will be deleted (i.e. archiving node)
# everything: 2 latest states will be kept; pruning at 10 block intervals.
# custom: allow pruning options to be manually specified through 'pruning-keep-recent', and 'pruning-interval'
pruning = "nothing"

# These are applied if and only if the pruning strategy is custom.
pruning-keep-recent = "0"
pruning-interval = "0"

# HaltHeight contains a non-zero block height at which a node will gracefully
# halt and shutdown that can be used to assist upgrades and testing.
#
# Note: Commitment of state will be attempted on the corresponding block.
halt-height = 0

# HaltTime contains a non-zero minimum block time (in Unix seconds) at which
# a node will gracefully halt and shutdown that can be used to assist upgrades
# and testing.
#
# Note: Commitment of state will be attempted on the corresponding block.
halt-time = 0

# MinRetainBlocks defines the minimum block height offset from the current
# block being committed, such that all blocks past this offset are pruned
# from CometBFT. It is used as part of the process of determining the
# ResponseCommit.RetainHeight value during ABCI Commit. A value of 0 indicates
# that no blocks should be pruned.
#
# This configuration value is only responsible for pruning CometBFT blocks.
# It has no bearing on application state pruning which is determined by the
# "pruning-*" configurations.
#
# Note: CometBFT block pruning is dependant on this parameter in conjunction
# with the unbonding (safety threshold) period, state pruning and state sync
# snapshot parameters to determine the correct minimum value of
# ResponseCommit.RetainHeight.
min-retain-blocks = 0

# InterBlockCache enables inter-block caching.
inter-block-cache = true

# IndexEvents defines the set of events in the form {eventType}.{attributeKey},
# which informs CometBFT what to index. If empty, all events will be indexed.
#
# Example:
# ["message.sender", "message.recipient"]
index-events = []

# IavlCacheSize set the size of the iavl tree cache (in number of nodes).
iavl-cache-size = 781250

# IAVLDisableFastNode enables or disables the fast node feature of IAVL.
# Default is false.
# 压测开启，用于快速查询
iavl-disable-fastnode = false

# AppDBBackend defines the database backend type to use for the application and snapshots DBs.
# An empty string indicates that a fallback will be used.
# The fallback is the db_backend value set in CometBFT's config.toml.
app-db-backend = ""

###############################################################################
###                    SeiDB / MemIAVL（P1 状态后端优化）                     ###
###############################################################################
# 用 memIAVL（内存 CoW 提交树 + 异步落盘）替换经典 iAVL 的同步提交，
# 目标：把 WorkingHash(687ms)+iAVL commit(1035ms)=1722ms 从关键路径压到几百 ms 内。
#
# ⚠️ 共识关键：memIAVL 与 iAVL 的状态根计算实现不同。本配置由 stresser 全节点统一生成、
#    全部走 memIAVL，故节点间一致；但 memIAVL 节点 与 iAVL 节点 不可混跑（app hash 不兼容）。
#    上线前必须做：4 节点 + 重启 1 节点 的 app hash 逐块对账。

[memiavl]
# 主开关：true 即整链状态承诺改用 memIAVL（GetStoreConfig 会据此把 Backend 切到 SeiDB、SC=memiavl）
enable = true
# 异步提交缓冲：>0 时 commit 只更内存树+算根，落盘后台化（把 commit 移出关键路径）。压测设 100。
async-commit-buffer = 100
# 内存节点缓存（条目数）
cache-size = 100000
# 快照：保留最近 1 份、每 10000 块出一次（压测够用）
snapshot-keep-recent = 1
snapshot-interval = 10000
# zero-copy 先关（更稳，mmap 零拷贝有额外约束，确认稳定后再开提速）
zero-copy = false

[seidb]
enabled = true
sc-backend = "memiavl"
# 首测先不开历史 State-Store（压测不查历史状态），隔离验证 memIAVL 提交树本身。
# 确认 memIAVL 稳定 + app hash 对账通过后，再开 ss-enable=true / ss-backend=pebbledb / ss-async-write-buffer 提历史查询性能。
ss-enable = false

###############################################################################
###                         Telemetry Configuration                         ###
###############################################################################

[telemetry]

# Prefixed with keys to separate services.
service-name = ""

# Enabled enables the application telemetry functionality. When enabled,
# an in-memory sink is also enabled by default. Operators may also enabled
# other sinks such as Prometheus.
enabled = true

# Enable prefixing gauge values with hostname.
enable-hostname = false

# Enable adding hostname to labels.
enable-hostname-label = false

# Enable adding service to labels.
enable-service-label = false

# PrometheusRetentionTime, when positive, enables a Prometheus metrics sink.
prometheus-retention-time = 60

# GlobalLabels defines a global set of name/value label tuples applied to all
# metrics emitted using the wrapper functions defined in telemetry package.
#
# Example:
# [["chain_id", "cosmoshub-1"]]
global-labels = [
]

# MetricsSink defines the type of metrics sink to use.
metrics-sink = ""

# StatsdAddr defines the address of a statsd server to send metrics to.
# Only utilized if MetricsSink is set to "statsd" or "dogstatsd".
statsd-addr = ""

# DatadogHostname defines the hostname to use when emitting metrics to
# Datadog. Only utilized if MetricsSink is set to "dogstatsd".
datadog-hostname = ""

###############################################################################
###                           API Configuration                             ###
###############################################################################

[api]

# Enable defines if the API server should be enabled.
enable = true

# Swagger defines if swagger documentation should automatically be registered.
swagger = true

# Address defines the API server to listen on.
address = "tcp://{{.IPListen}}:{{.Ports.API}}"

# MaxOpenConnections defines the number of maximum open connections.
max-open-connections = 1000

# RPCReadTimeout defines the CometBFT RPC read timeout (in seconds).
rpc-read-timeout = 10

# RPCWriteTimeout defines the CometBFT RPC write timeout (in seconds).
rpc-write-timeout = 0

# RPCMaxBodyBytes defines the CometBFT maximum request body (in bytes).
rpc-max-body-bytes = 1000000

# EnableUnsafeCORS defines if CORS should be enabled (unsafe - use it at your own risk).
enabled-unsafe-cors = true

###############################################################################
###                           gRPC Configuration                            ###
###############################################################################

[grpc]

# Enable defines if the gRPC server should be enabled.
enable = true

# Address defines the gRPC server address to bind to.
address = "{{.IPListen}}:{{.Ports.GRPC}}"

# MaxRecvMsgSize defines the max message size in bytes the server can receive.
# The default value is 10MB.
max-recv-msg-size = "10485760"

# MaxSendMsgSize defines the max message size in bytes the server can send.
# The default value is math.MaxInt32.
max-send-msg-size = "2147483647"

###############################################################################
###                        gRPC Web Configuration                           ###
###############################################################################

[grpc-web]

# GRPCWebEnable defines if the gRPC-web should be enabled.
# NOTE: gRPC must also be enabled, otherwise, this configuration is a no-op.
# NOTE: gRPC-Web uses the same address as the API server.
enable = true

###############################################################################
###                             EVM Configuration                           ###
###############################################################################

[evm]

# Tracer defines the 'vm.Tracer' type that the EVM will use when the node is run in
# debug mode. To enable tracing use the '--evm.tracer' flag when starting your node.
# Valid types are: json|struct|access_list|markdown
tracer = ""

# MaxTxGasWanted defines the gas wanted for each eth tx returned in ante handler in check tx mode.
max-tx-gas-wanted = 0

###############################################################################
###                           JSON RPC Configuration                        ###
###############################################################################

[json-rpc]

# Enable defines if the gRPC server should be enabled.
enable = true

# Address defines the EVM RPC HTTP server address to bind to.
address = "{{.IPListen}}:{{.Ports.EVMRPC}}"

# Address defines the EVM WebSocket server address to bind to.
ws-address = "{{.IPListen}}:{{.Ports.EVMWSPort}}"

# API defines a list of JSON-RPC namespaces that should be enabled
# Example: "eth,txpool,personal,net,debug,web3"
api = "eth,net,web3"

# GasCap sets a cap on gas that can be used in eth_call/estimateGas (0=infinite).
gas-cap = 25000000

# EVMTimeout is the global timeout for eth_call. Default: 5s.
evm-timeout = "5s"

# TxFeeCap is the global tx-fee cap for send transaction. Default: 1eth.
txfee-cap = 1

# FilterCap sets the global cap for total number of filters that can be created
filter-cap = 200

# FeeHistoryCap sets the global cap for total number of blocks that can be fetched
feehistory-cap = 100

# LogsCap defines the max number of results can be returned from single 'eth_getLogs' query.
logs-cap = 10000

# BlockRangeCap defines the max block range allowed for 'eth_getLogs' query.
block-range-cap = 10000

# HTTPTimeout is the read/write timeout of http json-rpc server.
http-timeout = "30s"

# HTTPIdleTimeout is the idle timeout of http json-rpc server.
http-idle-timeout = "2m0s"

# AllowUnprotectedTxs restricts unprotected (non EIP155 signed) transactions to be submitted via
# the node's RPC when the global parameter is disabled.
allow-unprotected-txs = true

# MaxOpenConnections sets the maximum number of simultaneous connections
# for the server listener.
max-open-connections = 0

# EnableIndexer enables the custom transaction indexer for the EVM (ethereum transactions).
enable-indexer = true

# AllowIndexerGap allow block gap for the custom transaction indexer for the EVM (ethereum transactions).
allow-indexer-gap = true

# Define if JSON-RPC metrics server should be enabled.
metrics = false

# MetricsAddress defines the EVM Metrics server address to bind to.
# Prometheus metrics path: /debug/metrics/prometheus
metrics-address = "127.0.0.1:6065"

# Maximum number of bytes returned from eth_call or similar invocations.
return-data-limit = 512000

###############################################################################
###                        State Sync Configuration                         ###
###############################################################################

# State sync snapshots allow other nodes to rapidly join the network without replaying historical
# blocks, instead downloading and applying a snapshot of the application state at a given height.
[state-sync]

# snapshot-interval specifies the block interval at which local state sync snapshots are
# taken (0 to disable).
snapshot-interval = 0

# snapshot-keep-recent specifies the number of recent snapshots to keep and serve (0 to keep all).
snapshot-keep-recent = 2

###############################################################################
###                              State Streaming                            ###
###############################################################################

# Streaming allows nodes to stream state to external systems.
[streaming]

# streaming.abci specifies the configuration for the ABCI Listener streaming service.
[streaming.abci]

# List of kv store keys to stream out via gRPC.
# The store key names MUST match the module's StoreKey name.
#
# Example:
# ["acc", "bank", "gov", "staking", "mint"[,...]]
# ["*"] to expose all keys.
keys = []

# The plugin name used for streaming via gRPC.
# Streaming is only enabled if this is set.
# Supported plugins: abci
plugin = ""

# stop-node-on-err specifies whether to stop the node on message delivery error.
stop-node-on-err = true

###############################################################################
###                         Mempool                                         ###
###############################################################################

[mempool]
# Setting max-txs to 0 will allow for a unbounded amount of transactions in the mempool.
# Setting max_txs to negative 1 (-1) will disable transactions from being inserted into the mempool (no-op mempool).
# Setting max_txs to a positive number (> 0) will limit the number of transactions in the mempool, by the specified amount.
#
# Note, this configuration only applies to SDK built-in app-side mempool
# implementations.
max-txs = 500000