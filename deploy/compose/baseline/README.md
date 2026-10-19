# Baseline 隔离验证栈（A3，本地非生产）

一页说明。本栈用于证明可复现的原生构建、专用 PostgreSQL 测试库，以及离线应用冒烟验证，并且不会触碰既有的 `deploy/compose/offline/` 栈、仓库根目录 `.env` 或任何用户数据卷。

<a id="what-it-is"></a>
## 它是什么

* `postgres:16-alpine`，带一个私有的 `gateway_test` 数据库。它**没有 `env_file`**：只有三个 PG_* 字段通过调用方 shell 的显式 `--env-file` 插值传入，因此数据库容器永远不会拿到更广范围的合成应用密钥（助记词、管理员密码、门户 JWT）。
* `gateway`：当前源码的**原生**运行时镜像，`GATEWAY_OFFLINE=true`，只接入内部网络，不发布任何端口。
* `test`（profile `test`）：当前源码的**原生**测试镜像，在同一内部网络内运行 `scripts/baseline/run-tests.sh`，并使用 `GOPROXY=off` 与 `GOTOOLCHAIN=local`。它的四个 DSN 变量来自受控的 `P0_DSN_FILE`，绝不会从宿主机继承。

隔离保证：没有顶层 `name:`，数据卷和网络也没有显式 `name:`（由 `-p` 项目前缀命名所有对象），不引用任何既有数据卷，也不回退到仓库根目录 `.env`。

**调用约定（Compose 不做校验）：** `P0_ENV_FILE` / `P0_DSN_FILE` / `P0_EVIDENCE_DIR` 必须是绝对路径。Compose 只做插值，不检查是否为绝对路径。请在从不 source 仓库根 `.env` 的 shell 中传入它们。

<a id="prepare-synthetic-credentials-only"></a>
## 准备（仅合成凭据）

```sh
umask 077
RUN="$(mktemp -d /tmp/opencode/gateway-a3-XXXXXX)"
mkdir -p "$RUN/env" "$RUN/evidence"
scripts/generate-offline-env.sh --path "$RUN/env/gateway.env"          # 0600, never overwrites
python3 scripts/baseline/make-dsn-env.py --env "$RUN/env/gateway.env" --out "$RUN/env/dsn.env"
```

<a id="build-network-allowed-for-dependencies-native-cgo-never-fallback"></a>
## 构建（依赖允许联网；原生 CGO，绝不使用 fallback）

```sh
TAG=a3-$(date +%s)
docker build --build-arg BUILD_JOBS=4 --target gotest  -t "gateway-baseline:${TAG}-test"    .
docker build                           --target runtime -t "gateway-baseline:${TAG}-runtime" .
```

<a id="run-the-suite-one-full-pass-evidence-lands-in-runevidence"></a>
## 运行测试套件（一次完整执行；证据落在 $RUN/evidence）

```sh
export P0_ENV_FILE="$RUN/env/gateway.env" P0_DSN_FILE="$RUN/env/dsn.env" \
       P0_TEST_IMAGE="gateway-baseline:${TAG}-test" P0_RUNTIME_IMAGE="gateway-baseline:${TAG}-runtime" \
       P0_EVIDENCE_DIR="$RUN/evidence" P0_TEST_DB=gateway_test
PROJ="p0b-a3-$(date +%s)"
compose() { docker compose -p "$PROJ" --env-file "$P0_ENV_FILE" -f deploy/compose/baseline/docker-compose.yml "$@"; }

compose up -d postgres --wait
compose --profile test run --rm test scripts/baseline/run-tests.sh --evidence /evidence
python3 scripts/baseline/verify-test-json.py \
  --json "$RUN/evidence/test.jsonl" --exit-code "$RUN/evidence/exit-code" \
  --vet-exit-code "$RUN/evidence/vet-exit-code" --build-exit-code "$RUN/evidence/build-exit-code" \
  --must-run scripts/baseline/must-run.txt --allow-skip scripts/baseline/allow-skip.txt \
  --summary "$RUN/evidence/summary.json"
```

退出码语义：

* `run-tests.sh`：`0` 表示全绿；否则返回 build、vet、test 中第一个非零退出码；`70` 仅用于缺失合成 DSN 变量（环境错误）；`64` 表示调用方式错误。数据库连接失败**不会**被重映射——它们会以普通的 test/vet 退出码呈现。
* `verify-test-json.py`：`0` 通过，`2` 门禁违规，`3` 证据错误（JSON 不可解析/非对象、事件流为空、must-run 列表为空）。门禁还会拒绝未到达终态事件（事件流被截断）的测试、缺少 `run` 事件或未以 `pass` 终态的 must-run 测试，以及没有包级完成 `pass` 的 must-run 包。
* 唯一被容忍的 skip 是 `core/repositories Test_Merchant`（见 `scripts/baseline/allow-skip.txt`）；它会被报告，但从不计为通过。

<a id="smoke-offline-app-no-secrets-in-output"></a>
## 冒烟（离线应用；输出中不含密钥）

```sh
compose up -d gateway --wait
docker exec "${PROJ}-gateway-1" curl -fsS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3001/
docker exec "${PROJ}-gateway-1" curl -fsS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3001/docs/integration-guide.md
docker exec "${PROJ}-gateway-1" curl -fsS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3001/docs/money-event-catalog.md
docker exec "${PROJ}-gateway-1" curl -s  -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3001/api/v1/common/readiness   # 401 without credentials
docker logs "${PROJ}-gateway-1" 2>&1 | grep 'Offline mode'
docker exec "${PROJ}-gateway-1" sh -c 'getent hosts example.com'          # must fail
docker exec "${PROJ}-gateway-1" sh -c 'curl --max-time 3 -fsS https://example.com'   # must fail
docker exec "${PROJ}-gateway-1" sh -c 'curl --max-time 3 -fsS http://93.184.216.34'  # DNS-independent probe
docker inspect "${PROJ}-gateway-1" --format '{{json .NetworkSettings.Networks}}'
```

<a id="ci-regression-workflow"></a>
## CI（Regression 工作流）

`.github/workflows/regression.yml` 把本 baseline 作为**必需**结论来运行：`gotest` 镜像从当前检出构建（`BUILD_JOBS=2`），专用 `postgres:16-alpine` 在内部网络启动，`scripts/baseline/run-tests.sh` 记录 build/vet/test 的原始退出码，只有 `scripts/baseline/verify-test-json.py` 决定通过/失败。门禁以 `if: always()` 运行（失败时证据仍保留），产物上传与清理步骤同样如此，清理只停止该 job 自己的 compose 项目。在耗时的原生构建之前，`docker compose … config --quiet` 预检会验证 baseline compose 文件所需的每个变量都已存在（它不打印内容，因此不会有解析后的密钥进入日志）。上传产物使用**显式文件白名单**——`go-version.txt`、`build.txt`、`build.stderr`、`vet.txt`、`vet.stderr`、`test.jsonl`、`test.stderr`、三个退出码文件与 `summary.json`——不对运行目录做递归通配，因此 env 文件与凭据永远不会被上传。`fallback-regression` 是补充项，不能掩盖原生失败。

该接线由 `scripts/baseline/ci_workflow_contract_test.go` 固化，它作为常规 Go 测试套件的一部分运行。

<a id="local-checks-for-the-baseline-tooling-itself"></a>
## baseline 工具自身的本地检查

```sh
python3 scripts/baseline/test_verify_test_json.py     # gate unit tests
sh scripts/baseline/test_run_tests.sh                 # runner stub tests
```

<a id="stop"></a>
## 停止

```sh
compose stop    # keeps this project's own volume and containers
```

在没有单独的、明确的资源销毁授权时，绝不使用 `down -v`、`volume rm`，也不要删除已存在的镜像/容器。

<a id="non-goals"></a>
## 非目标

不做业务逻辑、schema、HD 路径、品牌或许可变更；本里程碑不改 CI；不用于生产——这只是一个经过测试的本地 baseline。
