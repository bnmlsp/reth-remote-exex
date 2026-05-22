# Reth Remote ExEx gRPC 服务

将 [reth](https://github.com/paradigmxyz/reth) ExEx（Execution Extension）编译进 reth 二进制，在每个区块执行后通过 gRPC 实时推送完整区块数据（blocks + receipts + state diff）到外部消费者，使用 protobuf 序列化，任何语言均可接入。

目标版本：**reth v2.2.0**

---

## 架构说明

```
reth 节点进程
├── ExEx 钩子（同进程，极低延迟）
│     每个区块执行后收到 ExExNotification
│     ChainCommitted / ChainReorged / ChainReverted
│     包含：blocks + receipts + BundleState（state diff）
└── gRPC server（0.0.0.0:10000）
      └── RemoteExEx.Subscribe → stream Notification
            外部消费者（Go / Python / Rust / ...）订阅
```

**ExEx 不包含** call trace / internal transactions，如需请用 `debug_traceBlockByNumber`。

---

## 构建

### 前提

```bash
# Rust stable
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
source ~/.cargo/env
rustup default stable

# 系统依赖（Ubuntu/Debian）
sudo apt-get update
sudo apt-get install -y \
    build-essential pkg-config libssl-dev \
    libclang-dev protobuf-compiler cmake
```

### 编译

```bash
cargo build --release
# fat LTO + codegen-units=1，首次编译约 15-30 分钟
# 产出：target/release/exex（约 68MB，已 strip）
```

---

## 部署

### 替换二进制

```bash
# 停止节点（最多等 180 秒优雅关闭）
sudo systemctl stop reth

# 备份原始二进制
cp /home/ubuntu/eth/bin/reth /home/ubuntu/eth/bin/reth.bak

# 替换
cp target/release/exex /home/ubuntu/eth/bin/reth

# 启动
sudo systemctl start reth
```

### 回滚

```bash
sudo systemctl stop reth
cp /home/ubuntu/eth/bin/reth.bak /home/ubuntu/eth/bin/reth
sudo systemctl start reth
```

### 验证

```bash
# ExEx 已启动
sudo journalctl -u reth -n 50 | grep -i exex

# gRPC 端口监听
ss -tlnp | grep 10000
# 预期：LISTEN 0  128  0.0.0.0:10000  0.0.0.0:*

# 节点已同步
curl -s -X POST http://127.0.0.1:8545 \
  -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","method":"eth_syncing","params":[],"id":1}'
# 预期：{"result":false}
```

### systemd 服务配置参考

```ini
[Unit]
Description=reth Execution Layer Client
After=network.target
Wants=network.target

[Service]
User=ubuntu
Type=simple
Restart=always
RestartSec=10
ExecStart=/home/ubuntu/eth/bin/reth node \
    --datadir /home/ubuntu/eth/data/reth \
    --authrpc.jwtsecret /home/ubuntu/eth/cfg/jwt.hex \
    --authrpc.addr 0.0.0.0 \
    --authrpc.port 8551 \
    --http --http.addr 0.0.0.0 --http.port 8545 \
    --http.api eth,net,web3,txpool,debug,trace \
    --http.corsdomain "*" \
    --ws --ws.addr 0.0.0.0 --ws.port 8546 \
    --ws.api eth,net,web3,txpool,debug,trace \
    --port 30303 --addr 0.0.0.0 \
    --max-peers 100 \
    --rpc.max-response-size 160 \
    --rpc.max-connections 500 \
    --rpc.gascap 100000000 \
    --rpc.max-tracing-requests 8 \
    --metrics 127.0.0.1:9001 \
    --log.file.directory /home/ubuntu/eth/log/reth \
    -vvv
TimeoutStopSec=180
LimitNOFILE=1000000
LimitNPROC=1000000

[Install]
WantedBy=default.target
```

---

## Go Consumer 示例

示例代码位于 `examples/go-consumer/`，proto/gen/ 已预生成，clone 后直接可编译。

### 构建

```bash
cd examples/go-consumer
go build -o consumer .
```

### 运行

```bash
# 本机访问
./consumer 127.0.0.1:10000

# 外部访问
./consumer <节点IP>:10000
```

---

## 关键参数

| 参数 | 位置 | 当前值 | 说明 |
|------|------|--------|------|
| gRPC 监听地址 | `src/exex.rs` | `0.0.0.0:10000` | 改为 `[::1]:10000` 可限制仅本机访问 |
| broadcast 队列 | `src/exex.rs` | `16` | 慢消费者时丢消息，可适当调大 |
| per-client 队列 | `src/exex.rs` | `16` | 同上 |
| consumer 接收上限 | `examples/go-consumer/main.go` | `64MB` | 单条消息上限，主网单块一般不超过 20MB |

---

## 已知限制

1. **无认证**：明文无鉴权，建议内网部署或配置防火墙规则限制访问来源
2. **慢消费者丢消息**：broadcast channel 满时新消息被静默丢弃，消费者无法感知
3. **无断线重连**：consumer 断开后需业务代码自行实现重连逻辑
4. **无 internal tx**：ExEx 不提供 call trace，需要 `debug_traceBlockByNumber` 补充
