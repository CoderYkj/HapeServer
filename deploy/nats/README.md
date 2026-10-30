# NATS R3 for the WAL

Three JetStream nodes with a shared cluster name. Each node has its own data
volume; the WAL stream is created with `replicas=3` by `storage.walReplicas`.

## Local

```bash
docker compose -f deploy/nats/docker-compose.yml up -d
```

NATS client endpoints:

```text
nats://127.0.0.1:4221
nats://127.0.0.1:4222
nats://127.0.0.1:4223
```

Run the R3 test:

```bash
NATS_URL='nats://127.0.0.1:4221,nats://127.0.0.1:4222,nats://127.0.0.1:4223' \
NATS_REPLICAS=3 \
go test ./internal/wal -run TestJetStreamRoundTrip -v
```

## Kubernetes

Use a 3-replica StatefulSet with a headless Service. For ordinals `nats-0..2`:

```text
-cluster nats://0.0.0.0:6222
-cluster_name hape
--routes nats://nats-0.nats:6222,nats://nats-1.nats:6222,nats://nats-2.nats:6222
-js -sd /data
```

Point `storage.natsUrl` at all three pods and set `storage.walReplicas=3`.
R3 tolerates one NATS node loss; two losses make the WAL stream read-only.