# deploy/k8s

P1 占位。P0 不部署 K8s；这里将放：

- `base/`：命名空间、ConfigMap、Secret、ServiceAccount
- `shard/`：StatefulSet + headless Service + local PV
- `gateway/`：Deployment + HPA + L4 Service
- `infra/`：etcd / NATS / MongoDB（或托管引用）
- `overlays/<region>/`：区域差异与灰度

入口清单以架构文档 §11 为准。
