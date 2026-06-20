# Milton Prism Python — Dev Environment Setup

## MongoDB as a single-node replica set (required for transactions)

Motor's multi-document transactions require MongoDB to run as a replica set, even
in local development.  A single-node RS is trivial to configure and is the standard
dev setup.  Without it, `MotorTransactionManager.with_transaction` silently falls
back to running the operation without a transaction, which means the migration
service's state-machine writes are NOT atomic.

### Option A — bare mongod (no Docker)

```bash
# 1. Create a data directory
mkdir -p /tmp/rs0-data

# 2. Start mongod bound to the replica set name "rs0"
mongod --replSet rs0 --port 27017 --dbpath /tmp/rs0-data --fork --logpath /tmp/rs0.log

# 3. Initiate the replica set (one-time)
mongosh --eval "rs.initiate({_id: 'rs0', members: [{_id: 0, host: 'localhost:27017'}]})"

# 4. Verify
mongosh --eval "rs.status().ok"   # should print 1
```

To stop: `mongod --shutdown --dbpath /tmp/rs0-data`

### Option B — Docker

```bash
docker run -d --name mongo-rs \
  -p 27017:27017 \
  mongo:7 --replSet rs0

# Wait ~2 s then initiate
docker exec mongo-rs mongosh --eval \
  "rs.initiate({_id:'rs0',members:[{_id:0,host:'localhost:27017'}]})"
```

### Option C — Docker Compose (recommended for CI / team)

```yaml
# docker-compose.yml
services:
  mongo:
    image: mongo:7
    command: --replSet rs0 --bind_ip_all
    ports:
      - "27017:27017"
    healthcheck:
      test: mongosh --eval "rs.status().ok" --quiet
      interval: 5s
      retries: 10
    volumes:
      - mongo_data:/data/db

  mongo-init:
    image: mongo:7
    depends_on:
      mongo:
        condition: service_healthy
    restart: "no"
    entrypoint: >
      mongosh --host mongo --eval
      "rs.initiate({_id:'rs0',members:[{_id:0,host:'mongo:27017'}]})"

volumes:
  mongo_data:
```

```bash
docker compose up -d
```

---

## Environment variables

```bash
MONGO_URI=mongodb://localhost:27017/
MONGO_DATABASE=milton_prism_dev
GRPC_HOST=0.0.0.0
GRPC_PORT=50051
```

---

## Verifying transactions work

After starting MongoDB as a replica set, run the transaction tests:

```bash
cd python
MONGO_URI=mongodb://localhost:27017/ poetry run pytest shared/tests/test_transaction_manager.py -v
```

Expected output with a replica set:
```
test_transaction_manager.py::test_none_client_runs_fn_directly PASSED
test_transaction_manager.py::test_none_client_propagates_exception PASSED
test_transaction_manager.py::test_returns_fn_return_value PASSED
test_transaction_manager.py::test_commit_on_success PASSED
test_transaction_manager.py::test_rollback_on_exception PASSED
```

Without a replica set, `test_commit_on_success` and `test_rollback_on_exception` will
print `SKIPPED [MongoDB replica set not available]`.  The three unit tests always pass.
