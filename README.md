# Tailscale DERP 中继服务器 Docker 镜像

[English](README.en.md) | 中文

本项目提供了一个 Docker 镜像，用于快速部署可验证客户端的 Tailscale DERP 中继服务器，仅需公网 IP 即可部署。

## 快速开始

### 1. 下载配置文件

```bash
curl -O https://raw.githubusercontent.com/haukuen/derper/main/docker-compose.yaml
```

### 2. 获取 Tailscale Auth Key

为了让 DERP 服务器能够验证连接客户端的合法性，它本身必须登录到你的 Tailscale 网络。

1. 登录 [Tailscale 管理控制台 - Keys 页面](https://login.tailscale.com/admin/settings/keys)。
2. 点击 "Generate auth key"。
3. 建议设置：
    - **Reusable**: 勾选（防止容器重建后 Key 失效）
    - **Ephemeral**: 不勾选
    - **Tags**: 可选（例如 `tag:derper`，方便 ACL 管理）。
4. 复制生成的 `tskey-auth-xxxxxx` 开头的 Auth Key。


### 3. 修改配置

编辑 `docker-compose.yaml` 文件，填入 **公网 IP**。

在同级目录创建 `.env` 文件，将上一步获取的 **Auth Key** 写入其中：

```env
TS_AUTHKEY=tskey-auth-xxxxx
```


### 4. 启动服务

```bash
docker-compose up -d
```

### 5. 获取 CertName

查看容器日志获取证书名称：

```bash
docker logs derper
```

在日志末尾找到类似以下格式的 `CertName` 信息：

```
derper: Using self-signed certificate for IP address "YOUR_SERVER_PUBLIC_IP". Configure it in DERPMap using:
derper:   {"Name":"custom","RegionID":900,"HostName":"YOUR_SERVER_PUBLIC_IP","CertName":"sha256-raw:f829xxxxxxxxxxxxxx"}
```

复制 `sha256-raw:f829...` 这部分内容，后续配置会用到。

## 配置 Tailscale ACL

### 1. 访问管理控制台

登录 [Tailscale 管理控制台](https://login.tailscale.com/admin/acls)。

### 2. 编辑 ACL 配置

在 ACL JSON 配置中添加 `derpMap` 部分。以下是一个完整的配置示例：

**配置说明：**
- `OmitDefaultRegions`: 设置为 `true` 将禁用所有 Tailscale 官方中继（可选）
- `RegionID`: 自定义区域 ID，官方建议从 900 到 999 之间选择
- `RegionCode`: 自定义名称，会显示在 netcheck 中
- `HostName`: 你的中继服务器公网 IP 地址
- `CertName`: 粘贴上一步获取的证书名称

```json
{
  "derpMap": {
    "OmitDefaultRegions": true,
    "Regions": {
      "900": {
        "RegionID": 900,
        "RegionCode": "my-derp-1",
        "RegionName": "My First DERP",
        "Nodes": [
          {
            "Name": "my-node-1",
            "RegionID": 900,
            "HostName": "YOUR_SERVER_PUBLIC_IP",
            "DERPPort": 40007,
            "STUNPort": 40008,
            "CertName": "sha256-raw:f829..."
          }
        ]
      }
    }
  }
}
```
如需部署多个地区的中继，可添加 "901": { ... } 等配置

### 3. 保存配置

保存 ACL 配置使更改生效。

## 验证部署

在任意 Tailscale 设备上执行以下命令验证中继是否正常工作：

```bash
tailscale netcheck
```

预期结果：

1. DERP latency 输出中应该包含你自定义的 Region。
2. 该节点的延迟应该显示具体数值，而不是空白或错误。

## 注意事项

- 确保服务器的 40007/tcp 和 40008/udp 端口已开放。

## 参考

[Tailscale 配置文档](https://pkg.go.dev/tailscale.com/tailcfg#DERPRegion)
