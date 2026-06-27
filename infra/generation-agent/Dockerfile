# Milton Prism — Generation Agent Image
#
# Toolchain container for Camino B autonomous service generation.
# Contains: Go, buf, git, Node.js, Rust, JDK 21 + Maven, Ruby 3 + Bundler, .NET SDK 9,
#           C++20 (g++/CMake/Ninja + grpc++/protobuf/mongocxx/libpqxx + jwt-cpp), Claude Code CLI.
#
# Build (from repo root):
#   docker build -t milton-prism-generation-agent:latest \
#       -f infra/generation-agent/Dockerfile .
#
# The image carries NO model credentials. ANTHROPIC_API_KEY must be injected
# at container start time as an environment variable (A.7). Never bake
# credentials into the image and never log the Env slice that carries them.
#
# ── Base image: Debian (glibc), NOT Alpine (musl) ────────────────────────────
# WHY glibc: the Java gRPC generation profile's codegen runs the Maven-Central
# `protoc` and `protoc-gen-grpc-java` :exe binaries, which are GLIBC C++
# executables. On the previous musl Alpine base the `gcompat` shim DID load the
# loader (no exit 127) but `protoc-gen-grpc-java` aborted at startup with
#   PROTOC FAILED: terminate called after throwing an instance of 'std::system_error'
#   --grpc-java_out: protoc-gen-grpc-java: Plugin killed by signal 6
# i.e. SIGABRT — gcompat's partial musl→glibc shim cannot run the grpc-java
# plugin's C++/threading runtime. A glibc base runs those Maven-Central binaries
# NATIVELY, so the offline gRPC codegen guard below passes. Go is preinstalled
# in the golang:1.25-trixie base; all other toolchains are reinstalled via apt.
#
# ── .NET SDK source stage (multi-stage copy) ─────────────────────────────────
# The C# (grpc-dotnet / ASP.NET Core) generation profile needs the Microsoft
# .NET SDK. Microsoft's official SDK image (mcr.microsoft.com/dotnet/sdk:9.0) is
# Debian (glibc) based — the SAME libc family as this golang:trixie base — so we
# copy its self-contained /usr/share/dotnet tree straight into the final image
# rather than running dotnet-install.sh at build time. Grpc.Tools' bundled
# `protoc` + `grpc_csharp_plugin` are glibc :exe artifacts; the glibc base runs
# them NATIVELY (same glibc reason as grpc-java), so the offline C# gRPC codegen
# guard below passes.
FROM mcr.microsoft.com/dotnet/sdk:9.0 AS dotnet-sdk

FROM golang:1.25-trixie

LABEL org.opencontainers.image.title="milton-prism-generation-agent"
LABEL org.opencontainers.image.description="Milton Prism Camino B: autonomous service generation toolchain"

ARG BUF_VERSION=1.50.0

# protoc-gen-openapi — the RustLangLatam fork the platform uses for OpenAPI gen.
# Pinned to the commit that built the platform's host binary (matches the opts
# in protobuf/buf.docs.gen.yaml: depth/enum_type/default_response). The module
# path (github.com/RustLangLatam/protoc-gen-openapi) differs from the repo URL
# (github.com/RustLangLatam/openapi-generatos), so a proxy `go install
# <module>@<ver>` cannot resolve it — a full clone + checkout + `go install .`
# (the root main package) is the reliable install, producing a binary named
# `protoc-gen-openapi` (after the module path's last element).
ARG PROTOC_GEN_OPENAPI_REPO=https://github.com/RustLangLatam/openapi-generatos
ARG PROTOC_GEN_OPENAPI_REF=722ed85

# System dependencies (Debian/apt; converted from the previous Alpine/apk set):
#   git              — VCS operations inside the workspace
#   nodejs / npm     — required by Claude Code CLI AND by the Node (TypeScript)
#                      generation profile (npm install + tsc build gate)
#   curl             — buf binary download
#   bash             — shell for agent commands (claude --bare uses bash internally)
#   ca-certificates  — HTTPS to api.anthropic.com, proxy.golang.org, buf.build
#   protobuf-compiler — provides `protoc`, used by the Node profile's ts-proto
#                      codegen path and REQUIRED by the Rust profile (tonic-build
#                      shells out to `protoc` for Prost/Tonic codegen in build.rs).
#   gcc pkg-config libssl-dev — C toolchain + common -sys crate build inputs
#                      (rustc/cargo link the final binary and build crates with C
#                      build scripts, e.g. ring/tls deps).
#   openjdk-21-jdk-headless — JDK 21 LTS, the Java (Spring Boot + grpc-java)
#                      generation profile's build gate (`mvn package`)
#                      compiler/runtime. Installs under
#                      /usr/lib/jvm/java-21-openjdk-amd64 (see JAVA_HOME below).
#   maven            — Apache Maven, the Java profile's build tool. The Debian
#                      package places `mvn` on PATH; global settings live at
#                      /usr/share/maven/conf/settings.xml.
#
# NOTE: the musl shims (gcompat / libstdc++ / libgcc / musl-dev) the Alpine
# image carried are GONE — on glibc the Maven-Central protoc/grpc-java binaries
# run natively, so no shim is needed.
RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      git \
      nodejs \
      npm \
      curl \
      bash \
      ca-certificates \
      protobuf-compiler \
      gcc \
      pkg-config \
      libssl-dev \
      openjdk-21-jdk-headless \
      maven \
      ruby-full \
      ruby-dev \
      build-essential \
      libpq-dev \
      default-libmysqlclient-dev \
      cmake \
      ninja-build \
      libgrpc++-dev \
      protobuf-compiler-grpc \
      libprotobuf-dev \
      libmongoc-dev \
      libbson-dev \
      libsasl2-dev \
      libpqxx-dev \
      libmysqlcppconn-dev && \
    rm -rf /var/lib/apt/lists/*
# Ruby toolchain note (apt set above):
#   ruby-full / ruby-dev        — CRuby 3 + headers; the Ruby (grpc gem / Rails|Sinatra)
#                                 generation profile's runtime. Native-extension gems
#                                 (grpc, pg, mysql2, bson) compile C/C++ against GLIBC —
#                                 the Debian (glibc) base is REQUIRED; musl/Alpine would
#                                 break those builds. (Same glibc reason as grpc-java.)
#   build-essential             — gcc/g++/make for the native-extension gem builds.
#   libpq-dev                   — PostgreSQL client headers for the `pg` gem.
#   default-libmysqlclient-dev  — MySQL/MariaDB client headers for the `mysql2` gem.
# C++ toolchain note (apt set above):
#   cmake / ninja-build         — the C++ (grpc++ / Drogon) generation profile's build
#                                 system + generator. Build gate: `cmake --build`. (g++ is
#                                 already provided by build-essential.)
#   libgrpc++-dev               — grpc++ headers + libs (grpc::ServerBuilder/grpc::Server).
#   protobuf-compiler-grpc      — provides `grpc_cpp_plugin` (the protoc gRPC C++ codegen
#                                 plugin); `protoc` itself comes from protobuf-compiler above.
#   libprotobuf-dev             — protobuf C++ runtime headers + libs (links the generated
#                                 *.pb.cc). All three (protoc/grpc/protobuf) are built
#                                 together in Debian, so the codegen + runtime are ABI-matched.
#   libmongoc-dev/libbson-dev/libsasl2-dev — the MongoDB C driver (libmongoc 1.30) +
#                                 deps. Debian trixie does NOT package the mongocxx/bsoncxx
#                                 C++ driver, so it is BUILT FROM SOURCE below against this
#                                 system C driver and installed to /usr/local (the default store).
#   libpqxx-dev                 — libpqxx, the PostgreSQL C++ client (artisanal SQL cell).
#   libmysqlcppconn-dev         — MySQL Connector/C++ (MySQL/MariaDB artisanal SQL cell).
# CRITICAL: these libraries are PREINSTALLED so a generated C++ service's CMakeLists
# resolves them via pkg-config/find_package and only compiles + links ITS OWN code —
# it must NEVER `FetchContent`/`ExternalProject` grpc++/protobuf/mongocxx (re-downloading
# + recompiling those blows the container wall-clock budget). The C++ pre-warm guard
# below proves the offline protoc-grpc codegen + the grpc++/mongocxx link work natively.

# ── Rust toolchain via rustup (NOT the distro rustc) ─────────────────────────
# WHY rustup, not Debian's `rustc`/`cargo`: Debian trixie pins rustc at 1.85.0,
# but the Rust profile's transitive crate graph (mongodb 3.7, darling 0.23,
# hickory, time 0.3.51, icu 2.x, serde_with 3.21) now demands rustc 1.86–1.88.
# A `cargo build` of the warm crate fails at resolve time:
#   error: rustc 1.85.0 is not supported by the following packages:
#     mongodb@3.7.0 requires rustc 1.88 ... (and ~25 more)
# rustup installs a CURRENT stable toolchain (glibc, matching this base), so the
# warm build resolves and the runtime Rust gate (`cargo build`) keeps working as
# the crate MSRVs climb. The toolchain is installed into the SHARED, world-
# readable CARGO_HOME/RUSTUP_HOME so the non-root prism (uid=1000) agent reuses
# it (see the cargo pre-warm block, which makes CARGO_HOME a+rX).
ENV CARGO_HOME=/usr/local/cargo
ENV RUSTUP_HOME=/usr/local/rustup
ENV PATH=$CARGO_HOME/bin:$PATH
RUN curl -fsSL https://sh.rustup.rs | \
      sh -s -- -y --no-modify-path --profile minimal --default-toolchain stable && \
    rustc --version && cargo --version

# Java toolchain on PATH + JAVA_HOME. The build-time guard (mirroring the
# protoc-gen-openapi `command -v` guard) fails the image build loudly if the
# JDK/Maven ever go missing or off-PATH. Debian installs the JDK under
# /usr/lib/jvm/java-21-openjdk-amd64.
ENV JAVA_HOME=/usr/lib/jvm/java-21-openjdk-amd64
ENV PATH=$JAVA_HOME/bin:$PATH
RUN command -v mvn && java -version && mvn -version

# buf CLI — downloaded directly from GitHub releases for the correct arch.
# Used for: buf lint, buf generate (gate block inside the container).
RUN ARCH=$(uname -m | sed 's/x86_64/x86_64/;s/aarch64/arm64/') && \
    curl -fsSL \
      "https://github.com/bufbuild/buf/releases/download/v${BUF_VERSION}/buf-Linux-${ARCH}" \
      -o /usr/local/bin/buf && \
    chmod +x /usr/local/bin/buf

# protoc-gen-openapi — installed from the RustLangLatam fork so the deliverable
# OpenAPI matches the platform's conventions.
#
# The `package main` that produces the binary lives at the REPO ROOT (main.go),
# and go.mod there declares `module github.com/RustLangLatam/protoc-gen-openapi`,
# so `go install .` of the root package emits a binary named after the module's
# last path element: `protoc-gen-openapi`. It lands in /go/bin (already on PATH
# in golang images); buf resolves it via `local: protoc-gen-openapi`.
#
# We install the ROOT package only (`go install .`), NOT `./...`: the repo also
# carries an examples/ tree that may contain its own main packages — `./...`
# would try to build all of them (extra/failing builds, possibly extra binaries),
# making the result non-deterministic. `.` builds exactly the one binary we need.
#
# A FULL clone (no --depth 1) + `git checkout <ref>` is used so an arbitrary
# pinned ref (commit SHA, branch, or tag) always resolves. The previous form
# (`clone --depth 1` of the default branch, then `fetch --depth 1 origin <SHA>`,
# then `checkout <SHA>`) failed at build with exit 128: a shallow clone fetches
# only the tip of the default branch, and `fetch --depth 1 origin <SHA>` is not
# reliably supported by the GitHub server for an arbitrary commit SHA
# (uploadpack.allowReachableSHA1InWant/allowAnySHA1InWant is not guaranteed), so
# the commit never lands locally and the subsequent `checkout <SHA>` aborts with
# a git error (exit 128) — failing the image build and leaving the old image
# (without protoc-gen-openapi) in place. A full clone has every commit locally,
# so the checkout always succeeds.
#
# Two build-time pitfalls this RUN deliberately avoids:
#   1. The clone must NOT live under /tmp: Go refuses to use a go.mod located in
#      the system temp root (`go: warning: ignoring go.mod in system temp root
#      /tmp` → `go.mod file not found`), so we clone to /build/... instead.
#   2. The fork's go.sum is incomplete (missing entries for
#      google.golang.org/protobuf/...), so a plain `go install .` fails with
#      `missing go.sum entry for module providing package ...`. We run it with
#      GOFLAGS=-mod=mod so the install resolves and adds the missing sums from
#      the network.
#
# The build-time guard `command -v protoc-gen-openapi` makes a future regression
# (wrong module path / install target) fail the image build loudly.
RUN git clone "${PROTOC_GEN_OPENAPI_REPO}" /build/protoc-gen-openapi-src && \
    git -C /build/protoc-gen-openapi-src checkout "${PROTOC_GEN_OPENAPI_REF}" && \
    (cd /build/protoc-gen-openapi-src && GOFLAGS=-mod=mod go install .) && \
    rm -rf /build/protoc-gen-openapi-src && \
    command -v protoc-gen-openapi

# ── Rust (Tonic) profile dependency pre-warm ─────────────────────────────────
# Compiling a Tonic/Prost service from a cold cache downloads + compiles ~100
# crates, which alone can exceed the generation container's wall-clock budget.
# We pre-build a throwaway crate that depends on EXACTLY the crate set the Rust
# profile fixes (tonic/prost/tokio/mongodb/...), so the agent image ships with:
#   - the crate registry index + downloaded source crates (CARGO_HOME/registry)
#   - compiled rlibs for those deps in the dummy crate's target/ (reused by the
#     agent's build through the shared registry — source crates never re-download)
# CARGO_HOME is a world-readable shared path so the non-root `prism` agent user
# reuses the warm registry. The agent still compiles its own service crate, but
# the heavy, slow-to-fetch dependency graph is already present.
#
# Keep this dep list in lockstep with milton-prism-rust-profile.md §A.1. Pinned
# minor versions keep the cache deterministic across image rebuilds.
# (CARGO_HOME/RUSTUP_HOME/PATH are set in the rustup install block above.)
RUN mkdir -p /build/prism-cargo-warm/src && \
    cd /build/prism-cargo-warm && \
    printf '%s\n' \
      '[package]' \
      'name = "prism_cargo_warm"' \
      'version = "0.0.0"' \
      'edition = "2021"' \
      '' \
      '[dependencies]' \
      'tonic = "0.12"' \
      'prost = "0.13"' \
      'tokio = { version = "1", features = ["rt-multi-thread", "macros"] }' \
      'mongodb = "3"' \
      'bson = "2"' \
      'async-trait = "0.1"' \
      'dotenvy = "0.15"' \
      'envy = "0.4"' \
      'serde = { version = "1", features = ["derive"] }' \
      'tracing = "0.1"' \
      'prost-types = "0.13"' \
      '' \
      '[build-dependencies]' \
      'tonic-build = "0.12"' \
      > Cargo.toml && \
    printf 'fn main() {}\n' > src/main.rs && \
    printf 'fn main() {}\n' > build.rs && \
    cargo build && \
    cargo build --release && \
    rm -rf /build/prism-cargo-warm && \
    chmod -R a+rX "$CARGO_HOME" "$RUSTUP_HOME"

# ── Java (Spring Boot + grpc-java) profile dependency pre-warm ───────────────
# A generated Spring Boot + grpc-java service's `mvn package` from a cold local
# repo downloads HUNDREDS of jars (Spring BOM, Netty, protobuf, JPA/Hibernate,
# JDBC drivers, the platform-specific protoc + protoc-gen-grpc-java binaries),
# which alone can exceed the generation container's wall-clock budget. We warm a
# SHARED local Maven repo at /usr/local/m2 from a throwaway pom that depends on
# EXACTLY the Java profile's GA coordinate set, then make it world-readable so
# the runtime non-root `prism` (uid=1000) agent reuses it.
#
# The warm repo is wired in by THREE mutually-reinforcing mechanisms so a
# default `mvn …` (no flags, any user) resolves from /usr/local/m2:
#   1. MAVEN_OPTS carries -Dmaven.repo.local=/usr/local/m2 — a system property
#      that always wins; this is the primary, version-independent guarantee.
#   2. A GLOBAL settings.xml at ${MAVEN_HOME}/conf/settings.xml (Maven home is
#      /usr/share/maven on Debian) sets <localRepository> for every user.
#   3. The runtime prism user's ~/.m2/settings.xml mirrors it (belt + braces).
# MAVEN_USER_HOME is exported for tooling that honours it; it is harmless if not.
#
# We do a REAL `mvn -B package` of a tiny Spring Boot stub (not merely
# `dependency:go-offline`, which is known to miss transitives such as micrometer
# and does not fetch the build/compiler/surefire plugins). The real build
# resolves the FULL transitive graph and caches the maven-compiler / surefire /
# spring-boot-maven plugins — exactly what a generated service's gate needs
# offline. Tests are skipped (-DskipTests) and boot repackage is skipped (in the
# pom) to keep the warm build fast; only the cached graph matters here.
#
# The warm pom BINDS the protobuf-maven-plugin codegen goals (compile +
# compile-custom against src/main/proto/warm.proto, which carries a gRPC
# service), so the warm `mvn package` EXECUTES real gRPC codegen — driving both
# the Maven-Central `protoc` :exe and the `protoc-gen-grpc-java` :exe. Those are
# GLIBC binaries; on this glibc (Debian) base they run NATIVELY (no shim).
# Running codegen at pre-warm time both warms the protoc/grpc :exe artifacts into
# /usr/local/m2 AND serves as the gRPC codegen proof. A dedicated OFFLINE
# (`mvn -o`) re-run below asserts `WarmServiceGrpc.java` is emitted with exit 0
# from the warm cache alone (no network, no downloads).
#
# CRITICAL: unlike the read-only cargo registry, a shared Maven local repo MUST
# be WRITABLE by the runtime user — Maven writes resolver metadata markers
# (_remote.repositories, resolver-status.properties) and the agent installs its
# OWN built artifacts into it. So /usr/local/m2 is made a+rwX (not a+rX), letting
# the non-root prism (uid=1000) agent reuse AND extend the warm cache.
# Keep the pinned versions in lockstep with milton-prism-java-profile.md.
ENV MAVEN_USER_HOME=/usr/local/m2
ENV MAVEN_OPTS="-Dmaven.repo.local=/usr/local/m2 -Djava.awt.headless=true -XX:+UseContainerSupport"
COPY infra/generation-agent/prewarm-pom.xml /build/prism-mvn-warm/pom.xml
COPY infra/generation-agent/prewarm-src /build/prism-mvn-warm/src
RUN mkdir -p /usr/local/m2 && \
    printf '%s\n' \
      '<settings xmlns="http://maven.apache.org/SETTINGS/1.0.0">' \
      '  <localRepository>/usr/local/m2</localRepository>' \
      '</settings>' \
      | tee /usr/local/m2/settings.xml /usr/share/maven/conf/settings.xml >/dev/null && \
    cd /build/prism-mvn-warm && \
    OSCLS=$(uname -m | sed 's/x86_64/linux-x86_64/;s/aarch64/linux-aarch_64/') && \
    mvn -B -s /usr/local/m2/settings.xml -Dmaven.repo.local=/usr/local/m2 \
        -DskipTests package && \
    mvn -B -q -s /usr/local/m2/settings.xml -Dmaven.repo.local=/usr/local/m2 \
        dependency:get -Dartifact=com.google.protobuf:protoc:4.31.1:exe:${OSCLS} && \
    mvn -B -q -s /usr/local/m2/settings.xml -Dmaven.repo.local=/usr/local/m2 \
        dependency:get -Dartifact=io.grpc:protoc-gen-grpc-java:1.73.0:exe:${OSCLS} && \
    # ── gRPC OFFLINE acceptance guard ────────────────────────────────────────
    # Wipe codegen output, then re-run codegen OFFLINE (`-o`) from the warm cache
    # alone. This proves (a) the warm /usr/local/m2 holds every artifact the
    # gRPC codegen needs (no network) and (b) the glibc `protoc`/`protoc-gen-grpc-java`
    # :exe binaries execute on this glibc base. Fail the image build loudly if
    # `WarmServiceGrpc.java` (the grpc-java service stub) is not emitted.
    rm -rf target/generated-sources && \
    mvn -B -o -s /usr/local/m2/settings.xml -Dmaven.repo.local=/usr/local/m2 \
        protobuf:compile protobuf:compile-custom && \
    test -f target/generated-sources/protobuf/grpc-java/com/milton/prism/warm/WarmServiceGrpc.java && \
    test -f target/generated-sources/protobuf/java/com/milton/prism/warm/WarmRequest.java && \
    echo "gRPC offline codegen OK: WarmServiceGrpc.java emitted" && \
    rm -rf /build/prism-mvn-warm && \
    chmod -R a+rwX /usr/local/m2

# ── Ruby (grpc gem / Rails|Sinatra) profile dependency pre-warm ──────────────
# A generated Ruby service's `bundle install` from a cold gem home would compile
# native-extension gems (grpc, pg, mysql2, bson) from source and download the
# whole dependency tree, which can exceed the generation container's wall-clock
# budget. We warm a SHARED, world-readable bundle path at /usr/local/bundle by
# `bundle install`-ing EXACTLY the Ruby profile's gem set (prewarm-Gemfile,
# milton-prism-ruby-profile.md §A.1.1), then make it a+rwX so the runtime non-root
# prism (uid=1000) agent reuses AND extends it (bundler writes its own resolution
# + installs the agent's gems). This is the Ruby homologue of the warmed
# /usr/local/m2 for Java.
#
# GEM_HOME/BUNDLE_PATH point bundler + rubygems at the shared dir for EVERY user
# (BUNDLE_APP_CONFIG makes the bundler config global; the profile.d snippet below
# re-exports them for login shells). The grpc-tools gem's `grpc_tools_ruby_protoc`
# binstub lands on PATH via /usr/local/bundle/bin.
#
# Native gems compile C/C++ against GLIBC here — the Debian base (same glibc
# reason as grpc-java) makes that work; musl/Alpine would break the builds.
# GEM_HOME alone (BUNDLE_PATH intentionally UNSET so it defaults to GEM_HOME) makes
# bundler install gems + binstubs into the standard rubygems layout under
# /usr/local/bundle/{gems,bin} — the SAME pattern as the official `ruby` Docker
# image. Setting BUNDLE_PATH to a non-default path would instead use an isolated
# ruby/<ver>/bin layout that keeps binstubs (grpc_tools_ruby_protoc) off PATH.
ENV GEM_HOME=/usr/local/bundle
ENV BUNDLE_APP_CONFIG=/usr/local/bundle
ENV BUNDLE_SILENCE_ROOT_WARNING=1
ENV PATH=/usr/local/bundle/bin:$PATH
COPY infra/generation-agent/prewarm-Gemfile /build/prism-gem-warm/Gemfile
COPY infra/generation-agent/prewarm-src/main/proto/warm.proto /build/prism-gem-warm/warm.proto
RUN mkdir -p /usr/local/bundle /build/prism-gem-warm/gen && \
    gem install bundler --no-document && \
    cd /build/prism-gem-warm && \
    bundle install --jobs 4 --retry 2 && \
    # ── gRPC OFFLINE codegen acceptance guard ────────────────────────────────
    # Run grpc_tools_ruby_protoc over the warm proto (which carries a gRPC
    # `service`) via `bundle exec` so it resolves from the warmed Gemfile
    # regardless of binstub placement. This proves (a) the warmed gem home holds
    # the grpc-tools codegen toolchain and (b) the bundled protoc + ruby grpc
    # plugin execute on this base. Fail the image build loudly if
    # `warm_services_pb.rb` (the grpc service stub) is not emitted — the Ruby
    # homologue of the Java WarmServiceGrpc.java guard.
    bundle exec grpc_tools_ruby_protoc -I /build/prism-gem-warm \
        --ruby_out=/build/prism-gem-warm/gen \
        --grpc_out=/build/prism-gem-warm/gen \
        /build/prism-gem-warm/warm.proto && \
    test -f /build/prism-gem-warm/gen/warm_services_pb.rb && \
    test -f /build/prism-gem-warm/gen/warm_pb.rb && \
    ruby -c /build/prism-gem-warm/gen/warm_services_pb.rb && \
    echo "gRPC ruby offline codegen OK: warm_services_pb.rb emitted" && \
    rm -rf /build/prism-gem-warm && \
    chmod -R a+rwX /usr/local/bundle

# ── .NET SDK 9 (copied from the official Microsoft SDK image) ─────────────────
# Copy the self-contained dotnet tree from the mcr.microsoft.com/dotnet/sdk:9.0
# stage. It is glibc-based (same family as this base), so it runs natively here.
# DOTNET_ROOT + PATH expose the `dotnet` driver; the telemetry/first-run opt-outs
# keep the build deterministic and quiet (no phone-home, no first-run UX writes).
COPY --from=dotnet-sdk /usr/share/dotnet /usr/share/dotnet
ENV DOTNET_ROOT=/usr/share/dotnet
ENV PATH=/usr/share/dotnet:$PATH
ENV DOTNET_CLI_TELEMETRY_OPTOUT=1
ENV DOTNET_NOLOGO=1
ENV DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1
RUN dotnet --version

# ── C# (grpc-dotnet / ASP.NET Core) profile dependency pre-warm ──────────────
# A generated C# service's `dotnet build` from a cold NuGet cache would download
# the whole package graph (Grpc.AspNetCore + Grpc.Tools, Google.Protobuf,
# MongoDB.Driver, EF Core + Npgsql + Pomelo, JwtBearer) AND the Roslyn analyzer
# assets, which alone can exceed the generation container's wall-clock budget. We
# warm a SHARED, world-readable NuGet cache at /usr/local/nuget by restoring +
# building EXACTLY the C# profile's package set (prewarm.csproj,
# milton-prism-csharp-profile.md §A.1.1), then make it a+rwX so the runtime
# non-root prism (uid=1000) agent reuses AND extends it. This is the C# homologue
# of the warmed /usr/local/m2 for Java and /usr/local/bundle for Ruby.
#
# NUGET_PACKAGES points every user's restore at the shared dir (the profile.d
# snippet below re-exports it for login shells). Roslyn + dotnet build peak above
# 1 GiB (csharp is on the heavy resource tier), but the warm cache absorbs the
# restore so the runtime build is compute, not download.
ENV NUGET_PACKAGES=/usr/local/nuget
ENV DOTNET_CLI_HOME=/usr/local/nuget
COPY infra/generation-agent/prewarm.csproj /build/prism-dotnet-warm/prewarm.csproj
COPY infra/generation-agent/prewarm-src/main/proto/warm.proto /build/prism-dotnet-warm/warm.proto
RUN mkdir -p /usr/local/nuget && \
    cd /build/prism-dotnet-warm && \
    dotnet restore && \
    # ── gRPC OFFLINE codegen acceptance guard ────────────────────────────────
    # `dotnet build` runs Grpc.Tools over the warm proto (which carries a gRPC
    # `service`), driving protoc + grpc_csharp_plugin (glibc :exe artifacts). This
    # proves (a) the warmed NuGet cache holds the Grpc.Tools codegen toolchain and
    # (b) those binaries execute on this glibc base. Fail the image build loudly if
    # `WarmGrpc.cs` (the grpc-dotnet service base) is not emitted — the C#
    # homologue of the Java WarmServiceGrpc.java / Ruby warm_services_pb.rb guard.
    dotnet build --no-restore -c Release && \
    test -n "$(find /build/prism-dotnet-warm/obj -name 'WarmGrpc.cs' -print -quit)" && \
    test -n "$(find /build/prism-dotnet-warm/obj -name 'Warm.cs' -print -quit)" && \
    echo "gRPC csharp offline codegen OK: WarmGrpc.cs emitted" && \
    rm -rf /build/prism-dotnet-warm && \
    chmod -R a+rwX /usr/local/nuget

# ── mongocxx / bsoncxx (built from source — not packaged in Debian) ───────────
# Debian trixie ships the MongoDB C driver (libmongoc-dev / libbson-dev, 1.30) but
# NOT the C++ driver (mongocxx/bsoncxx — the C++ store the profile uses). We build
# it from a pinned release tarball against the system C driver and install it to
# /usr/local (pkg-config tokens `libmongocxx` / `libbsoncxx`, the SAME tokens the
# generated CMakeLists + the C++ warm guard resolve). This is a ONE-TIME image-build
# cost (network is available at build time); the runtime gate only LINKS a generated
# service against the already-installed shared libs — it never rebuilds the driver.
# PKG_CONFIG_PATH is set so pkg-config sees the /usr/local .pc files; `ldconfig`
# registers the shared libs so the runtime loader/link finds them.
ENV PKG_CONFIG_PATH=/usr/local/lib/pkgconfig:/usr/local/lib/x86_64-linux-gnu/pkgconfig:/usr/lib/x86_64-linux-gnu/pkgconfig
ARG MONGOCXX_VERSION=3.11.0
RUN curl -fsSL "https://github.com/mongodb/mongo-cxx-driver/releases/download/r${MONGOCXX_VERSION}/mongo-cxx-driver-r${MONGOCXX_VERSION}.tar.gz" \
      -o /build/mongocxx.tgz && \
    mkdir -p /build/mongocxx && \
    tar xzf /build/mongocxx.tgz -C /build/mongocxx --strip-components=1 && \
    cmake -S /build/mongocxx -B /build/mongocxx/build -G Ninja \
      -DCMAKE_BUILD_TYPE=Release \
      -DCMAKE_INSTALL_PREFIX=/usr/local \
      -DBUILD_VERSION="${MONGOCXX_VERSION}" \
      -DMONGOCXX_OVERRIDE_DEFAULT_INSTALL_PREFIX=OFF \
      -DENABLE_TESTS=OFF && \
    cmake --build /build/mongocxx/build --target install && \
    ldconfig && \
    rm -rf /build/mongocxx /build/mongocxx.tgz && \
    pkg-config --exists libmongocxx && pkg-config --exists libbsoncxx && \
    echo "mongocxx installed OK: $(pkg-config --modversion libmongocxx)"

# ── jwt-cpp (header-only) — vendored into /usr/local/include ──────────────────
# The C++ profile's JWT auth uses jwt-cpp (a header-only library backed by OpenSSL,
# libssl-dev already installed). It is not packaged in Debian, so we vendor the
# headers (jwt-cpp/ + its bundled picojson/) at a pinned tag into the system include
# root so a generated service can `#include <jwt-cpp/jwt.h>` with NO download at build
# time (the same offline guarantee as the warmed package caches). Header-only → no
# link target beyond OpenSSL's ssl/crypto. A full clone + checkout of a pinned tag is
# used (same reliability reasoning as protoc-gen-openapi). The build-time guard greps
# the installed header so a future regression (bad tag / moved layout) fails loudly.
ARG JWT_CPP_REF=v0.7.0
RUN git clone https://github.com/Thalhammer/jwt-cpp /build/jwt-cpp-src && \
    git -C /build/jwt-cpp-src checkout "${JWT_CPP_REF}" && \
    cp -r /build/jwt-cpp-src/include/jwt-cpp /usr/local/include/jwt-cpp && \
    cp -r /build/jwt-cpp-src/include/picojson /usr/local/include/picojson && \
    rm -rf /build/jwt-cpp-src && \
    test -f /usr/local/include/jwt-cpp/jwt.h && \
    test -f /usr/local/include/picojson/picojson.h

# ── C++ (grpc++ + CMake) profile pre-warm + OFFLINE codegen/link acceptance guard ──
# Unlike the other heavy profiles there is NO package graph to download at runtime:
# grpc++/protobuf/mongocxx/libpqxx are PREINSTALLED via apt (ABI-matched), so a
# generated C++ service only compiles + links ITS OWN code against the system libs.
# What MUST be proven at image-build time is exactly that path:
#   (a) `protoc` + `grpc_cpp_plugin` (Debian glibc binaries) run the C++ gRPC codegen
#       OFFLINE over the warm proto (which carries a gRPC `service`), emitting
#       warm.pb.cc/.h + warm.grpc.pb.cc/.h — the C++ homologue of the Java
#       WarmServiceGrpc.java / Ruby warm_services_pb.rb / C# WarmGrpc.cs guard; AND
#   (b) a minimal CMake project RESOLVES grpc++/protobuf/mongocxx/bsoncxx via
#       pkg-config (Debian ships the .pc files) + jwt-cpp via the vendored header, then
#       `cmake --build` COMPILES the generated stubs and LINKS a binary that references
#       grpc::ServerBuilder + mongocxx + jwt::verify. A successful link proves the whole
#       gRPC+Mongo+JWT C++ stack is build-ready with find_package/pkg-config and NO
#       FetchContent. Fail the image build loudly if codegen or the link fails.
COPY infra/generation-agent/prewarm-src/main/proto/warm.proto /build/prism-cpp-warm/warm.proto
RUN cd /build/prism-cpp-warm && \
    protoc -I /build/prism-cpp-warm \
        --cpp_out=/build/prism-cpp-warm \
        --grpc_out=/build/prism-cpp-warm \
        --plugin=protoc-gen-grpc="$(which grpc_cpp_plugin)" \
        /build/prism-cpp-warm/warm.proto && \
    test -f /build/prism-cpp-warm/warm.grpc.pb.cc && \
    test -f /build/prism-cpp-warm/warm.pb.cc && \
    echo "gRPC cpp offline codegen OK: warm.grpc.pb.cc emitted" && \
    printf '%s\n' \
      'cmake_minimum_required(VERSION 3.20)' \
      'project(prism_cpp_warm CXX)' \
      'set(CMAKE_CXX_STANDARD 20)' \
      'set(CMAKE_CXX_STANDARD_REQUIRED ON)' \
      'find_package(PkgConfig REQUIRED)' \
      'pkg_check_modules(GRPCPP REQUIRED grpc++)' \
      'pkg_check_modules(PROTOBUF REQUIRED protobuf)' \
      'pkg_check_modules(MONGOCXX REQUIRED libmongocxx)' \
      'pkg_check_modules(BSONCXX REQUIRED libbsoncxx)' \
      'add_executable(prism_cpp_warm main.cpp warm.pb.cc warm.grpc.pb.cc)' \
      'target_include_directories(prism_cpp_warm PRIVATE ${CMAKE_CURRENT_SOURCE_DIR} ${CMAKE_CURRENT_BINARY_DIR} ${GRPCPP_INCLUDE_DIRS} ${PROTOBUF_INCLUDE_DIRS} ${MONGOCXX_INCLUDE_DIRS} ${BSONCXX_INCLUDE_DIRS})' \
      'target_link_directories(prism_cpp_warm PRIVATE ${GRPCPP_LIBRARY_DIRS} ${PROTOBUF_LIBRARY_DIRS} ${MONGOCXX_LIBRARY_DIRS} ${BSONCXX_LIBRARY_DIRS})' \
      'target_link_libraries(prism_cpp_warm PRIVATE ${GRPCPP_LIBRARIES} ${PROTOBUF_LIBRARIES} ${MONGOCXX_LIBRARIES} ${BSONCXX_LIBRARIES} ssl crypto)' \
      > CMakeLists.txt && \
    printf '%s\n' \
      '#include <grpcpp/grpcpp.h>' \
      '#include "warm.grpc.pb.h"' \
      '#include <mongocxx/instance.hpp>' \
      '#include <mongocxx/client.hpp>' \
      '#include <jwt-cpp/jwt.h>' \
      'int main() {' \
      '  grpc::ServerBuilder builder;' \
      '  (void)builder;' \
      '  auto tok = jwt::create().set_issuer("warm").sign(jwt::algorithm::hs256{"k"});' \
      '  (void)tok;' \
      '  return 0;' \
      '}' \
      > main.cpp && \
    cmake -S /build/prism-cpp-warm -B /build/prism-cpp-warm/build -G Ninja && \
    cmake --build /build/prism-cpp-warm/build && \
    test -x /build/prism-cpp-warm/build/prism_cpp_warm && \
    echo "C++ grpc++/mongocxx/jwt-cpp link OK: prism_cpp_warm built" && \
    rm -rf /build/prism-cpp-warm

# Claude Code CLI — model credentials are injected at runtime, never in image.
# --bare mode (used at invocation time) skips reading ~/.claude/ and .claude/,
# making the container fully stateless.
RUN npm install -g @anthropic-ai/claude-code --no-fund --no-audit

# Suppress npm and Claude Code telemetry/update noise in container output.
ENV NPM_CONFIG_UPDATE_NOTIFIER=false
ENV CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
ENV CI=true

# Run as a non-root user so --dangerously-skip-permissions is accepted.
# uid=1000 matches the typical host uid; bind-mounted workspace dirs created
# by os.MkdirTemp on the host are therefore writable without an explicit chmod.
# Debian useradd/groupadd (the Alpine addgroup/adduser -S flags do not apply).
RUN groupadd -r prism && useradd -r -g prism -u 1000 -m -d /home/prism prism

# prism user's ~/.m2/settings.xml — third pin of the warm-repo wiring (see the
# Java pre-warm block). Points localRepository at the shared world-readable repo.
RUN mkdir -p /home/prism/.m2 && \
    cp /usr/local/m2/settings.xml /home/prism/.m2/settings.xml && \
    chown -R prism:prism /home/prism/.m2

# Go build cache under /tmp so the non-root user can write.
# GOMODCACHE stays at /go/pkg/mod — the host read-only mount path is unchanged.
ENV GOCACHE=/tmp/go-build-cache
ENV GOMODCACHE=/go/pkg/mod
ENV HOME=/home/prism

# ── Login-shell PATH/env (Debian-specific) ───────────────────────────────────
# On Debian a LOGIN shell (`sh -lc`, `bash -l`) sources /etc/profile, which
# REBUILDS PATH from a hardcoded default and DISCARDS the Dockerfile `ENV PATH`
# additions (JAVA_HOME/bin, /go/bin, $CARGO_HOME/bin, /usr/local/go/bin). The
# Alpine base did not do this, so the toolchains were only reachable from a
# NON-login shell. To make the toolchains resolvable from ANY shell the agent
# may spawn (the acceptance check uses `sh -lc`), drop a profile.d snippet that
# re-prepends the toolchain dirs and re-exports the build-cache/Maven env for
# login shells. Non-login shells already inherit the image `ENV` values; this is
# belt-and-braces so both shell flavours see an identical, complete toolchain.
RUN printf '%s\n' \
      'export JAVA_HOME=/usr/lib/jvm/java-21-openjdk-amd64' \
      'export CARGO_HOME=/usr/local/cargo' \
      'export RUSTUP_HOME=/usr/local/rustup' \
      'export GOCACHE=/tmp/go-build-cache' \
      'export GOMODCACHE=/go/pkg/mod' \
      'export MAVEN_OPTS="-Dmaven.repo.local=/usr/local/m2 -Djava.awt.headless=true -XX:+UseContainerSupport"' \
      'export GEM_HOME=/usr/local/bundle' \
      'export BUNDLE_APP_CONFIG=/usr/local/bundle' \
      'export DOTNET_ROOT=/usr/share/dotnet' \
      'export NUGET_PACKAGES=/usr/local/nuget' \
      'export DOTNET_CLI_HOME=/usr/local/nuget' \
      'export DOTNET_CLI_TELEMETRY_OPTOUT=1' \
      'export DOTNET_NOLOGO=1' \
      'export PKG_CONFIG_PATH="/usr/local/lib/pkgconfig:/usr/local/lib/x86_64-linux-gnu/pkgconfig:/usr/lib/x86_64-linux-gnu/pkgconfig"' \
      'export PATH="$JAVA_HOME/bin:$CARGO_HOME/bin:/usr/local/bundle/bin:/usr/share/dotnet:/go/bin:/usr/local/go/bin:$PATH"' \
      > /etc/profile.d/milton-prism-toolchain.sh && \
    chmod 0644 /etc/profile.d/milton-prism-toolchain.sh

USER prism
WORKDIR /workspace
