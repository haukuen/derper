# Tailscale DERP 中继服务器 Docker 镜像

本项目提供了一个 Docker 镜像，用于快速部署可验证客户端的 Tailscale DERP 中继服务器，仅需公网 IP 即可部署。

## 快速开始

### 1. 下载配置文件

```bash
curl -O https://raw.githubusercontent.com/haukuen/derper/main/docker-compose.yaml
```

### 2. 修改配置

编辑 `docker-compose.yaml` 文件，将 `DERP_HOSTNAME` 设置为你的服务器公网 IP 地址。

### 3. 启动服务

```bash
docker-compose up -d
```

### 4. 获取 CertName

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

在输出的 DERP 延迟部分，你应该能看到刚刚添加的自定义中继节点。

## 注意事项

- 确保服务器的 40007/tcp 和 40008/udp 端口已开放。