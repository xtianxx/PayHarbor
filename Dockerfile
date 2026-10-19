# ENV-02 offline development environment.
#
# This image builds REAL Trust Wallet Core from the pinned submodule revision and
# links the gateway with CGO against it. The `walletcorefallback` build tag is
# never used here, and the offline runtime switch only removes chain listeners and
# periodic money/event workers — it never bypasses the native library requirement.
#
# Stage graph:
#   walletcore -> builds Trust Wallet Core + Rust/Protobuf deps, emits a slim
#                 artifact tree (headers, static archives, `tw` Go module)
#   gobuild    -> pinned Go toolchain, CGO build of the gateway
#   gotest     -> same image content as gobuild, used to run `go test` / `go vet`
#                 inside the image instead of installing a toolchain on the host
#   runtime    -> non-root image with the binary, templates, and static assets
#
# Only linux/amd64 is supported (matching the project's WSL2/Ubuntu dev target).

ARG UBUNTU_VERSION=24.04
ARG GO_VERSION=1.25.4
ARG GO_SHA256=9fa5ffeda4170de60f67f3aa0f824e426421ba724c21e133c1e35d6159ca1bec

# ---------------------------------------------------------------------------
# Stage 1: Trust Wallet Core (native build)
# ---------------------------------------------------------------------------
FROM ubuntu:${UBUNTU_VERSION} AS walletcore

# Build parallelism. The base image reports every host core, and this repository's
# native build (clang translation units, protobuf, and especially rustc) can exhaust
# a 15 GiB developer host when run unbounded. Override with
# `--build-arg BUILD_JOBS=<n>`; the default keeps peak memory predictable.
ARG BUILD_JOBS=4
ENV BUILD_JOBS=${BUILD_JOBS}
ENV CMAKE_BUILD_PARALLEL_LEVEL=${BUILD_JOBS}
# cargo (used by tools/rust-bindgen and codegen-v2) honours CARGO_BUILD_JOBS.
ENV CARGO_BUILD_JOBS=${BUILD_JOBS}

ENV DEBIAN_FRONTEND=noninteractive

# Package set mirrors third_party/trustwallet/wallet-core/tools/install-sys-dependencies-linux
# plus the tools that script assumes are already present (build-essential, clang,
# cmake) and `unzip`, which tools/download-dependencies needs to unpack the
# nlohmann/json archive on the native path. Deliberate deviation: this stage does
# NOT run the vendored sys-deps script, because it also installs coverage/lint-only
# packages (llvm-14, clang-tidy-14, and a hand-downloaded lcov 1.15) that this build
# does not need. CMake requires clang (`FATAL_ERROR "You should use clang compiler"`),
# Boost, and Ruby for wallet-core's codegen scripts.
RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential \
      gcc-multilib \
      g++-multilib \
      clang \
      cmake \
      ninja-build \
      libboost-all-dev \
      ruby-full \
      pkg-config \
      unzip \
      ca-certificates \
      curl \
      wget \
      git \
      python3 \
    && rm -rf /var/lib/apt/lists/*

# Pinned submodule source comes from the build context (initialize it with
# `git submodule update --init --recursive third_party/trustwallet/wallet-core`).
COPY third_party/trustwallet/wallet-core /wc/third_party/trustwallet/wallet-core

WORKDIR /wc/third_party/trustwallet/wallet-core

RUN test -f CMakeLists.txt && test -f samples/go/go.mod || { \
      echo "wallet-core submodule is missing from the build context; run:" >&2; \
      echo "  git submodule update --init --recursive third_party/trustwallet/wallet-core" >&2; \
      exit 1; \
    }

# Rust nightly toolchain + cbindgen, exactly as tools/install-rust-dependencies expects.
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \
      | sh -s -- -y --profile minimal --no-modify-path
ENV CARGO_HOME=/root/.cargo
ENV RUSTUP_HOME=/root/.rustup
ENV PATH=/root/.cargo/bin:$PATH

# Same sequence as scripts/build_wallet_core.sh (minus the coverage-only apt step):
# local protobuf/gtest/check, rust toolchain targets, native codegen, then the
# clang/CMake build of TrustWalletCore. The vendored script falls back to -j4 on
# Linux (it reads sysctl, unavailable here); this build uses the configurable
# BUILD_JOBS value instead of every host core.
RUN tools/install-dependencies \
    && tools/install-rust-dependencies \
    && tools/generate-files native \
    && cmake -H. -Bbuild -DCMAKE_BUILD_TYPE=Release -DCMAKE_C_COMPILER=clang -DCMAKE_CXX_COMPILER=clang++ \
    && cmake --build build --target TrustWalletCore -j "${BUILD_JOBS}"

# Slim artifact tree for the Go stage: public headers, static archives in the
# exact -L locations the repo's #cgo LDFLAGS expect (build/, build/local/lib/,
# build/trezor-crypto/), and the `tw` Go module that go.mod replaces.
RUN mkdir -p /wc-slim/build \
    && cp -r include /wc-slim/include \
    && find build -name '*.a' -exec cp --parents {} /wc-slim/ \; \
    && if [ -d build/local/include ]; then cp -r build/local/include /wc-slim/build/local/include; fi \
    && mkdir -p /wc-slim/samples \
    && cp -r samples/go /wc-slim/samples/go \
    && test -f /wc-slim/build/libTrustWalletCore.a \
    && test -f /wc-slim/samples/go/go.mod \
    && echo "wallet-core artifacts:" && find /wc-slim -name '*.a' | sort

# ---------------------------------------------------------------------------
# Stage 2: Go build (CGO, native Trust Wallet Core)
# ---------------------------------------------------------------------------
FROM ubuntu:${UBUNTU_VERSION} AS gobuild

ARG GO_VERSION
ARG GO_SHA256
ARG TARGETARCH

ENV DEBIAN_FRONTEND=noninteractive

RUN case "${TARGETARCH:-amd64}" in \
      amd64) ;; \
      *) echo "this environment only supports linux/amd64 (got ${TARGETARCH})" >&2; exit 1 ;; \
    esac

RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential \
      g++ \
      ca-certificates \
      curl \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL "https://dl.google.com/go/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tgz \
    && echo "${GO_SHA256}  /tmp/go.tgz" | sha256sum -c - \
    && tar -C /usr/local -xzf /tmp/go.tgz \
    && rm /tmp/go.tgz \
    && /usr/local/go/bin/go version

ENV PATH=/usr/local/go/bin:$PATH
# The Go version is pinned by go.mod; never let the toolchain auto-download another one.
ENV GOTOOLCHAIN=local
ENV CGO_ENABLED=1

WORKDIR /src

COPY go.mod go.sum ./
# go.mod replaces `tw` with ./third_party/trustwallet/wallet-core/samples/go, so the
# module graph cannot resolve before that directory exists.
COPY third_party/trustwallet/wallet-core/samples/go/ third_party/trustwallet/wallet-core/samples/go/
RUN go mod download

COPY . .
# Real native artifacts from stage 1 (headers + static libs + `tw` module).
COPY --from=walletcore /wc-slim/ third_party/trustwallet/wallet-core/

# No build tags: this is the native provider build, not the fallback provider.
RUN go build -trimpath -o /out/gateway .

# Build-time smoke check: the binary must start (dynamic loader + linked native
# libraries resolve) and stop only because configuration is absent. A missing
# shared library or a broken Trust Wallet Core link fails here.
RUN set -eu; \
    output="$(/out/gateway 2>&1 || true)"; \
    printf '%s\n' "$output" | head -3; \
    printf '%s\n' "$output" | grep -q 'DATABASE_URL is required' || { \
      echo "gateway binary did not start cleanly; native library linkage is broken" >&2; \
      exit 1; \
    }

RUN set -eu; \
    if ldd /out/gateway | grep -q 'not found'; then \
      echo "gateway binary has unresolved shared libraries:" >&2; \
      ldd /out/gateway | grep 'not found' >&2; \
      exit 1; \
    fi; \
    ls -l /out/gateway

# ---------------------------------------------------------------------------
# Stage 3: Test runner (reproducible entry point; no host toolchain required)
# ---------------------------------------------------------------------------
# Carries the Go toolchain, the warm module cache, the full source tree, and the
# native Trust Wallet Core artifacts, so the repository's Go test suite and vet
# run identically to CI without installing anything on the developer host:
#   docker build --build-arg BUILD_JOBS=4 --target gotest -t gateway-offline:test .
#   docker run --rm gateway-offline:test go test -p=1 -count=1 ./...
#   docker run --rm gateway-offline:test go vet ./...
FROM gobuild AS gotest

CMD ["go", "test", "-p=1", "-count=1", "./..."]

# ---------------------------------------------------------------------------
# Stage 4: Runtime
# ---------------------------------------------------------------------------
FROM ubuntu:${UBUNTU_VERSION} AS runtime

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates \
      libstdc++6 \
      curl \
      passwd \
      tzdata \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=gobuild /out/gateway /app/gateway
# Templates and static assets are read from disk at runtime (html.New("./views", ".html")).
COPY views/ /app/views/
COPY static/ /app/static/
# Markdown docs endpoints read these two files from disk (api/handlers/docs.go).
COPY docs/integration-guide.md docs/money-event-catalog.md /app/docs/

RUN useradd --uid 10001 --create-home --shell /usr/sbin/nologin gateway \
    && chown -R gateway:gateway /app

USER gateway

# Offline development defaults. Secrets and DATABASE_URL arrive through the
# environment (compose env_file), never baked into the image.
ENV APP_ENV=development \
    GATEWAY_OFFLINE=true \
    PORT=:3001

EXPOSE 3001

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD curl -fsS -o /dev/null http://127.0.0.1:3001/ || exit 1

ENTRYPOINT ["/app/gateway"]
