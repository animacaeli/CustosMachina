# k3s 调研 spike（P8-M3.1）

> 日期：2026-10-06 · 环境：macOS + OrbStack docker（k3s v1.31.2+k3s1 容器化）
> 目的：M3.2/M3.3 动工前的四项判定——k3s 可跑性 / client-go 选型 / 滚动语义 / Ingress canary 权重。
> **结论先行：四项全部验证通过，M3.2 可以开工。**

## 一、验证结果

### 1. k3s 容器化运行 ✅

```bash
docker run -d --name custos-k3s --privileged \
  -p 6443:6443 -p 18080:80 -p 18443:443 \
  <镜像源>/rancher/k3s:v1.31.2-k3s1 server --disable traefik
# kubeconfig：docker exec custos-k3s cat /etc/rancher/k3s/k3s.yaml（server 已是 127.0.0.1:6443）
```

- OrbStack 内核（7.0.11-orbstack）直接 Ready，node/containerd 正常。
- `--disable traefik`：k3s 自带 traefik 不支持 nginx 式 canary annotation，换 ingress-nginx。

### 2. client-go 选型 ✅

- **选型结论：官方 `k8s.io/client-go`（clientset 裸用）**——足够覆盖平台需求（Deployment/Service/Ingress CRUD + rollout 观察），不需要 server-side apply 库/Rust 系轻量库。v0.31.2 与 k3s v1.31 匹配无兼容问题。
- kubeconfig 直连（`clientcmd.BuildConfigFromFlags`）→ ns/Deployment（含探针+资源限额）/Service/Ingress 创建全部成功——**compose 语义翻译的对照面**（镜像/端口/healthcheck→readinessProbe/资源限制）在 API 层完整成立。
- 依赖体量：go mod tidy 拉全依赖约 2 分钟，编译正常——**注意主仓 backend 引入时锁定 k8s.io/* 版本与目标集群兼容**（client-server 版本偏差容忍 ±1 minor）。

### 3. 滚动更新（蓝绿→rollout 翻译依据）✅

```bash
kubectl -n custos-spike set image deploy/spike-app web=<镜像>:alpine
# rollout 耗时 2s；get rs：新 RS 接管 1/1，旧 RS 缩 0/0
```

- 镜像 tag 变更 → 新 RS 起_pod → readiness 探针过 → 切流量 → 缩旧（默认 maxSurge 25%/maxUnavailable 25%）。
- **语义对照**：compose 蓝绿的"健康检查通过后切域名"≈ K8s readiness 门控的自动滚动；drain ≈ 旧 RS 缩容。**回滚 = rollout undo（revision 历史内置）**，比 compose 蓝绿的"切回旧色域"更省——CanaryRenderer 翻译时回滚路径直接走 Deployment rollout undo。
- **注意**：client-go 判断 rollout 完成的条件是 `Generation == ObservedGeneration && UpdatedReplicas == Replicas && AvailableReplicas == Replicas`（spike 代码已验证）；镜像 tag 不变时 Update 是 no-op（不会触发滚动）。

### 4. Ingress canary annotation 权重 ✅（核心验证）

ingress-nginx v1.12.1（hostNetwork 80）+ 双 Deployment（主/canary 同 host 不同 Service）：

```yaml
# canary ingress（主 ingress 同 host+path 正常创建）
metadata:
  annotations:
    nginx.ingress.kubernetes.io/canary: "true"
    nginx.ingress.kubernetes.io/canary-weight: "30"
```

**实测流量分布**（按两版本响应字节差异统计，20 次请求）：

| canary-weight | 实测分布 | 期望 |
|---|---|---|
| 30 | 14 : 6（70/30） | 70/30 ✅ |
| 50（annotate 热改） | 12 : 8 | 50/50（小样本波动，动态生效 ✅） |

- **权重热调即时生效**（annotate 后约 6s，无需人工 reload）——**平台灰度比例滑杆可直接映射此 annotation**。
- v1.12 起 nginx.conf 无静态 upstream（Lua 动态 backend），**不能靠 grep 配置文件排障**，用响应差异/kubectl-plugin 看实际分布。

## 二、环境坑清单（国内网络实况，M3.2 部署向导的输入）

1. **Docker Hub 全断期**（registry-1.docker.io EOF），daocloud 镜像源**间歇可用**（多试几次）；**阿里云 `registry.cn-hangzhou.aliyuncs.com/google_containers/` 稳定可用**（pause、ingress-nginx 都有）——**M3.2 镜像清单默认走 aliyun google_containers + daocloud 兜底**。
2. **k3s 容器化跑 pod 需导入 pause sandbox 镜像**（容器化 k3s 不自带 airgap tar）：本机 pull → save → cp → `ctr -n k8s.io images import` → **tag 必须带 `docker.io/` 前缀**（`docker.io/rancher/mirrored-pause:3.6`）否则 kubelet 仍去拉。pause 二进制不可用 shell 脚本伪造（sandbox 容器退 = 全 pod 崩）。
3. **ingress-nginx 最小部署**：Deployment（hostNetwork:true + hostPort 80）+ IngressClass + **RBAC 完整清单**（pods/services/endpoints/endpointslices/secrets/configmaps/namespaces/nodes/events/ingresses/ingressclasses/leases——缺 endpointslices/secrets 时 404 无 backend）。
4. **宿主机端口映射陷阱**：OrbStack 的 `-p 18080:80` 在容器 hostNetwork 下不可靠（打到别的进程返回 Go 风格 404）——验证流量**在容器内打 127.0.0.1:80**。
5. k3s 容器内无 curl（用 busybox wget --header）。

## 三、对 M3.2/M3.3 的直接输入

- **部署翻译表**（compose → K8s）：image→containers[].image；ports→containerPort+Service port；healthcheck→readinessProbe（HTTPGet）；deploy.resources→Resources；环境变量→env。
- **蓝绿→rollout**：发布=改镜像 tag；回滚=rollout undo；状态=Deployment status 三条件。
- **灰度→canary**：主/canary 双 Service + canary Ingress annotation，平台比例滑杆写 canary-weight（热生效）。
- **域名→Ingress**：host 规则 + ingressClassName: nginx。
- kubeconfig 交互安全：平台的 k3s target 存 kubeconfig（AES 落库，复用 IM 凭证加密通道），client-go 按项目惰性构建 clientset。

## 四、遗留到 M3.2 的事

- 多副本 Deployment 的 Service 负载均衡验证（spike 单副本足够回答语义问题）。
- k3s **裸机安装路径**（非容器化）：`INSTALL_K3S_SKIP_DOWNLOAD` 离线安装 + daocloud/aliun 镜像预热——部署向导的真机部分。
- 观测栈 DaemonSet 化（node-exporter/fluent-bit）模板。
