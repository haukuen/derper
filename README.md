# Tailscale 自建 DERP 中继

[English](README.en.md) | 中文

> **适用范围**:本项目面向**多个相互独立 tailnet 共用中继**的场景(如与朋友各自持有独立 Tailscale 网络时共享中继带宽)。若仅为单一 tailnet 自用,无需自建 DERP——请直接使用官方 [Peer Relay](https://tailscale.com/docs/features/peer-relay) 功能。

本项目提供两个 Docker 镜像,用于搭建可被**多个相互独立 tailnet 共用**的 Tailscale DERP 中继服务。准入控制基于 node key 白名单,由独立的准入控制器集中管理;derper 通过 `--verify-client-url` 协议向其查询每个客户端的接入许可。


## 破坏性变更

- **移除 `TS_AUTHKEY` / `--verify-clients` 模式**:该模式通过本机 tailscaled 的 netmap 验证客户端,仅适用于单一 tailnet,且要求 TUN 设备与 `NET_ADMIN` 权限,与多 tailnet 共享场景不兼容。既有部署请迁移至 `VERIFY_CLIENT_URL`。
- **准入改为 `--verify-client-url`**:derper 将每个新连接的 node key 提交准入控制器核验。中继服务器不再运行 tailscaled。

## 部署

### 1. 获取配置文件

```bash
curl -O https://raw.githubusercontent.com/haukuen/derper/main/docker-compose.yaml
curl -o whitelist.txt https://raw.githubusercontent.com/haukuen/derper/main/whitelist.example.txt
```

### 2. 配置白名单(两种方式选其一)

**方式 A:Tailscale API 自动同步(推荐)**——成员 tailnet 创建一个只读 OAuth client,控制器定时拉取其设备列表,自动维护该 tailnet 全部设备的 node key:新设备自动进名单,设备移出 tailnet 自动撤销。配置后通常无需再手动维护 `whitelist.txt`。

在 [Tailscale 管理后台](https://console.tailscale.com/admin/settings/trust-credentials) 生成 OAuth client(client ID + client secret),勾选只读权限 **`devices:core:read`**,然后写入 `docker-compose.yaml`:

```yaml
  admission-controller:
    environment:
      - TS_SYNC_CLIENTS=k1234567890abcdef:tskey-client-xxxx,k2345:tskey-client-yyyy
      - TS_SYNC_INTERVAL=5m   # 可选,默认 5 分钟
```

> **凭证安全**:只接受 OAuth client。**不要使用全权限 API access token**——那种 key 拥有整个 tailnet 的控制权(改 ACL、删设备、签发密钥),绝不应交给第三方服务器。多个成员 tailnet 用逗号分隔,各自生成一个 client。

**方式 B:手动维护**——在任一节点上执行以下命令,获取该 tailnet **全部节点**的 node key:

Linux / macOS:

```bash
tailscale status --json | grep -o 'nodekey:[0-9a-zA-Z]*' | sort -u
```

将 key 逐行写入 `whitelist.txt`:

```
nodekey:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
nodekey:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
```

编辑保存即时生效,无需重启。两种方式可并存,放行名单取并集。

### 3. 配置服务器地址

编辑 `docker-compose.yaml`,将 `DERP_HOSTNAME` 设置为服务器**公网 IP**(默认使用自签证书)或**域名**(可启用 Let's Encrypt,见[证书配置](#证书配置可选))。

### 4. 启动

```bash
docker compose up -d
```

将启动两个容器:`admission-controller`(准入控制器)与 `derper`(中继)。

### 5. 获取证书指纹(公网 IP 场景)

使用公网 IP 时,derper 自动生成自签证书,需将其指纹填入客户端 DERP map:

```bash
docker logs derper
```

日志中的关键行:

```
derper: Using self-signed certificate for IP address "YOUR_SERVER_PUBLIC_IP". Configure it in DERPMap using:
derper:   {"Name":"custom","RegionID":900,"HostName":"YOUR_SERVER_PUBLIC_IP","CertName":"sha256-raw:f829xxxxxxxxxxxxxx"}
```

记录 `sha256-raw:...` 的完整值。

> **升级提示**:如果旧部署使用 `./derper-certs` bind mount,新版本的证书存储改为 named volume,首次启动会重新生成自签证书,`CertName` 指纹随之变化——需重新从日志提取并更新各 tailnet 的 derpMap。

## 客户端配置

每个 tailnet 的管理员在 [Tailscale 管理后台](https://console.tailscale.com/admin/acls/file) 的 ACL 中添加相同的 `derpMap`:

```json
{
  "derpMap": {
    "OmitDefaultRegions": false,
    "Regions": {
      "900": {
        "RegionID": 900,
        "RegionCode": "my-derp-1",
        "RegionName": "My DERP",
        "Nodes": [
          {
            "Name": "my-node-1",
            "RegionID": 900,
            "HostName": "YOUR_SERVER_PUBLIC_IP",
            "DERPPort": 40007,
            "STUNPort": 40008,
            "CertName": "sha256-raw:f829...",
            "IPv4": "YOUR_SERVER_PUBLIC_IP"
          }
        ]
      }
    }
  }
}
```

字段说明:

- `RegionID`:自定义,使用 900~999;多台中继各使用不同 Region ID。
- `DERPPort` / `STUNPort`:与服务端 `DERP_PORT` / `STUN_PORT` 保持一致。
- `CertName`:仅公网 IP(自签证书)场景需要;域名 + Let's Encrypt 场景省略。
- `OmitDefaultRegions`:设为 `true` 可禁用官方中继(可选)。客户端经 netcheck 测速后在官方与自建节点间自动选择延迟最低者,不强制独占。

## 验证

任一已加入 DERP map 的节点上执行:

```bash
tailscale netcheck       # 自定义 Region 应显示具体延迟
```

服务端侧,准入控制器日志(`docker logs admission-controller`)记录每次拒绝:`DENY nodekey=... source=...`;每个 key 首次放行时也会记录一条 `ALLOW ... (first admission since start)`,便于审计。

## 成员管理

**新增成员**:

1. 方式 A(同步):成员 tailnet 管理员生成一个 `devices:core:read` 只读 OAuth client 提供给你,追加到 `TS_SYNC_CLIENTS`,该 tailnet 全部设备自动进名单。
2. 方式 B(手动):成员提供 node key(获取方式见[配置白名单](#2-配置白名单方式两种选其一)),追加至 `whitelist.txt` 并保存。
3. 该成员所在 tailnet 的管理员将全部中继节点加入其 DERP map。

**移除成员**:方式 A 将对应 client 从 `TS_SYNC_CLIENTS` 移除并重建控制器(或直接将设备移出其 tailnet);方式 B 从 `whitelist.txt` 删除对应 key 并保存。已建立的连接在断开后无法重连。

## Tailscale API 自动同步

部署时的推荐配置(见[配置白名单](#2-配置白名单方式两种选其一)方式 A),此处补充行为细节:

- 同步结果仅存内存,不写入 `whitelist.txt`;手动名单与同步名单取并集。控制器重启后需等首轮同步完成(约数秒),期间 fail-closed,客户端会自动重试,无感知。
- 某个来源同步失败时**保留上一次名单**并记录错误(`GET /status` 可见),不会因 API 故障放行陌生人,也不会误清空名单。
- 成员吊销凭证后同步停止,已同步名单保持不变,可随时改用手动方式维护。

## 已知限制

- **准入仅在建连时核验**:derper 只在客户端建立新连接时查询一次准入控制器。吊销某个 key 后,该设备**已建立的连接会持续到自然断开**,不会被立即踢出;其重连会被拒绝。
- **重新登录会更换 node key**:设备重新认证后 node key 随之变化。方式 A(同步)自动跟进,无感知;方式 B(手动)需要成员重新提交新 key 并更新 `whitelist.txt`,否则会被拒绝。
- **准入接口无鉴权**:`/verify` 是明文 HTTP 且不带鉴权,只应部署在 derper 可达的私网/compose 网络内,切勿发布到公网。`docker-compose.yaml` 已默认将 8081 仅绑定到宿主机回环地址(`curl 127.0.0.1:8081/status` 可查看运行版本与各来源健康状态)。

## 环境变量

### derper

必填:

| 变量 | 说明 |
|---|---|
| `DERP_HOSTNAME` | 公网 IP 或域名 |
| `VERIFY_CLIENT_URL` | 准入控制器地址:`http://admission-controller:8081/verify` |

可选:

| 变量 | 默认值 | 说明 |
|---|---|---|
| `VERIFY_CLIENT_FAIL_OPEN` | `false` | 控制器不可达时是否放行新客户端。默认拒绝(fail-closed);上游 derper 默认值为放行,此处强制收敛为拒绝,如需放行需显式设置 `true` |
| `WAIT_FOR_CONTROLLER` | 空 | `host:port`,derper 启动前等待控制器健康检查通过(compose 网络内使用) |
| `DERP_PORT` | `40007` | DERP TLS 监听端口 |
| `STUN_PORT` | `40008` | STUN UDP 监听端口 |
| `DERP_CERTMODE` | `manual` | `manual`(自签/自带证书)或 `letsencrypt`(需域名) |
| `ACME_EMAIL` | 空 | letsencrypt 模式下的 ACME 账户邮箱 |
| `DERP_EXTRA_ARGS` | 空 | 透传给 derper 的额外参数,如 `--rate-config /etc/derper/rate-config.json` |

### admission-controller

可选:

| 变量 | 默认值 | 说明 |
|---|---|---|
| `ADMIT_LISTEN` | `:8081` | HTTP 监听地址 |
| `ADMIT_WHITELIST` | `/etc/derper/whitelist.txt` | 白名单文件路径 |
| `TS_SYNC_CLIENTS` | 空 | Tailscale 自动同步的 OAuth client 列表,`clientID:clientSecret`,逗号分隔;需 `devices:core:read` 只读权限 |
| `TS_SYNC_INTERVAL` | `5m` | 同步轮询间隔 |


## 证书配置(可选)

具备域名且 80/443 端口可达时,可使用 Let's Encrypt 证书,客户端 DERP map 无需 `CertName`:

```yaml
  derper:
    environment:
      - DERP_HOSTNAME=derp.example.com
      - DERP_CERTMODE=letsencrypt
      - ACME_EMAIL=you@example.com
```

## 多节点部署

- 每台中继服务器运行一个 derper 容器,`VERIFY_CLIENT_URL` 统一指向同一控制器。



## 特性

- **跨 tailnet 流量隔离**:DERP 仅转发以目标 node key 寻址的 WireGuard 密文;不具备目标 tailnet 会话密钥的第三方无法解密,白名单内成员之间亦然。
- **在线状态不可见**:连接状态广播(WatchConns)仅对持有 mesh key 的中继间节点开放,客户端无法枚举其他成员。
- **未授权访问拒绝**:node key 不在白名单的连接被直接拒绝;控制器不可达时按 fail-closed 策略同样拒绝。
- **带宽治理**:可启用 derper 实验性限速(`DERP_EXTRA_ARGS=--rate-config ...`)约束单个成员的带宽占用。

## 参考

- [derper 官方 README](https://github.com/tailscale/tailscale/blob/main/cmd/derper/README.md)
- [Tailscale 自建 DERP 文档](https://tailscale.com/kb/1232/derp-servers)
- [tailcfg.DERPAdmitClientRequest](https://pkg.go.dev/tailscale.com/tailcfg#DERPAdmitClientRequest)
