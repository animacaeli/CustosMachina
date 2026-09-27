# compose 部署约定与 nginx 接入规范

> 第四阶段 M5 文档（docs/plan-phase4-runtime.md 第三节第 5 项）：蓝绿之外 nginx 不扩功能，
> 本文把既有的隐式约定写成接入规范。业务项目接入平台部署前应对照本文自查。

## 一、隔离域命名（平台管理，业务方不取名）

| 域名 | 用途 | 生成处 |
|---|---|---|
| `<proj>-prod-blue` / `<proj>-prod-green` | 正式环境蓝绿双域 | release 蓝绿发布 |
| `<proj>-canary` | 灰度实例 | release 灰度发布 |
| `<proj>-test-<slot>` | 测试槽位（dev1/dev2…） | slots 模块 |

`<proj>` 为项目名经 `strx.NormalizeName` 归一化（小写、非法字符转 `-`）。
所有 compose 文件落目标机 `/opt/custos-machina/compose/<域名>/compose.yaml`。

## 二、nginx 接入约定

平台管理的 nginx 配置只有一个文件：目标机的
`/opt/custos-machina/canary/<proj>.conf`（宿主 nginx 与 nginx 容器两种承载均探测支持，
写入后 `nginx -t` 通过才 reload，失败自动还原备份）。**该文件整份由平台渲染，不要手工编辑。**

### 1. external network（既定接入规范）

nginx 容器与业务容器必须在同一 docker external network，nginx 才能按容器名解析 upstream：

```yaml
# 业务的 compose 文件
networks:
  default:
    name: custos-shared
    external: true
```

### 2. upstream 用容器名

compose 项目 `<proj>-prod-blue` 的 `server` 服务容器名为 `<proj>-prod-blue-server-1`。
平台渲染的 conf 中：

```nginx
upstream <proj>-prod-upstream { server <proj>-prod-blue-server-1:80; }  # 活跃颜色域
upstream <proj>-canary-upstream { server <proj>-canary-server-1:80; }   # 有灰度策略时
```

**入口服务名约定为 `server`**（canary/蓝绿渲染都按此拼容器名；其他名字的服务不会被引流）。

### 3. 业务 server 块引用平台变量（自行管理域名/证书的 server 块）

平台 conf 是 http 级片段，业务 server 块按需引用：

```nginx
server {
    listen 443 ssl;
    server_name app.example.com;
    # ...证书等自行配置...
    location / {
        proxy_pass http://<proj>-prod-upstream;   # 正式流量（蓝绿切换 = 平台改写 upstream + reload）
        # 灰度命中时切 canary（无灰度策略可省略）：
        # set $backend <proj>-prod-upstream;
        # if ($<proj>_canary_hit) { set $backend <proj>-canary-upstream; }
        # if ($<proj>_traffic_split = canary) { set $backend <proj>-canary-upstream; }
        # proxy_pass http://$backend;
    }
}
```

灰度变量说明：`$<proj>_canary_hit`（请求头策略命中为 1）、
`$<proj>_traffic_split`（流量策略，值 canary/stable）。
测试入口：`canary-test.<proj>.local:18999`（同机 `curl --resolve canary-test.<proj>.local:18999:127.0.0.1 http://...:18999/` 验证分流；非 80，避免任意 Host 头访问）。

### 4. 为什么不做运行时 resolver 解析

蓝绿/灰度切换都是**整份重写 conf + reload**：reload 后 nginx 对 upstream 容器名重新解析，
不需要 `resolver 127.0.0.11` 运行时解析方案（该方案对 upstream 内 server 域名不生效，
是常见踩坑点）。顺序约定：**先起新容器、再写 conf**——`nginx -t` 天然拦截引用了
不存在容器的残缺配置。

## 三、蓝绿接入自查清单（项目侧）

1. 入口走反代，不占宿主端口（仅暴露容器网络）；
2. `server` 服务定义 healthcheck（蓝绿健康门禁依赖它：全部容器 running 且
   定义了 healthcheck 的必须 healthy，超时 3 分钟判失败、发布中止）；
3. 无状态（会话外置到 Redis/DB）——双颜色域共存期共用存储；
4. 应用处理 SIGTERM 优雅退出（drain 窗口默认 30 秒）；
5. 长连接（SSE/WebSocket）客户端必须实现重连——drain 超时后旧颜色域销毁，长连接被硬切；
6. DB 迁移只加不改不删（加表/加可空列）——否则回滚（重新发布旧 tag）可能不兼容旧代码，
   需人工评估。

## 四、compose 期能力边界（不自建清单）

以下能力 compose 期**不做**（做了就是自建半个 K8s），见计划文档第一节：

- 滚动更新（以蓝绿的"整体切换"替代；实例级滚动等 k3s Deployment）；
- 服务发现/网络（external network + 容器名，属文档约定非代码问题）；
- nginx 全量管理（只管 `<proj>.conf` 整份 + 上述变量；ingress 等 k3s 期换 ingress）；
- 多机调度（单机 compose；触发判据满足后转 k3s 主线）。
