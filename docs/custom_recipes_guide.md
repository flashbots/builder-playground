# Custom Recipe YAML Guide

This is the full reference for the YAML recipe format. For a gentler intro, see [custom_recipes.md](custom_recipes.md).

---

## Top-level fields

```yaml
base: opstack                        # required — l1 | opstack | buildernet
description: "opstack, 2s slots, my token contracts predeployed"
hidden: true                         # skip this in `playground recipes` listings
base_args:                           # same flags you'd pass on the CLI
  - --slot-time-sec
  - "2"
predeploys: predeploys.json          # extra contracts into L2 genesis (L2 bases only)
setup:                               # runs before any service starts, in recipe dir
  - ./build.sh
recipe:
  my-component:
    ...
```

### `base_args`

Passes CLI flags to the base recipe, same as running `playground start l1 --slot-time-sec 2`.

```yaml
base: l1
base_args:
  - --slot-time-sec
  - "2"
  - --num-validators
  - "128"
```

### `setup`

Runs in order before any service starts, from the recipe's directory. Each command must exit 0 — if one fails, everything stops.

```yaml
setup:
  - go build -o ./mybin ./cmd/mybin
  - ./init-data.sh
```

### `predeploys`

Path to a JSON file (relative to the recipe file) with contracts to inject into the L2 genesis. Only meaningful for `opstack` and `buildernet` bases.

```yaml
base: opstack
predeploys: contracts/predeploys.json
```

---

## Components and services

The `recipe` map keys are component names. Each component can be removed outright or have individual services modified.

```yaml
recipe:
  <component-name>:
    remove: true   # removes the whole component and all its services
    services:
      <service-name>:
        ...
```

Removing a component also cleans up any `depends_on` and `{{Service "..."}}` references to it in other services.

---

## Service fields

### `remove`

```yaml
recipe:
  prometheus:
    services:
      prometheus:
        remove: true
```

Same cleanup of references applies as with component removal.

### `image` and `tag`

Either or both can be set independently.

```yaml
services:
  el:
    image: ghcr.io/my-org/reth-fork
    tag: v1.2.3
```

### `entrypoint`

Replaces the container entrypoint.

```yaml
services:
  el:
    entrypoint: /usr/local/bin/reth-debug
```

### `args` vs `replace_args`

**`args`** replaces the entire argument list for the service.

```yaml
services:
  el:
    args:
      - --datadir=/data
      - --http
      - --http.port=8545
```

**`replace_args`** patches specific flag values in the existing list without touching everything else. Provide flag-value pairs:

```yaml
services:
  el:
    replace_args:
      - --http.port
      - "9545"
      - --authrpc.port
      - "8552"
```

> `args` and `replace_args` are mutually exclusive — pick one per service. `replace_args` expects pairs; an odd number of entries logs a warning.

### `env`

Merges into the service's environment — existing variables are kept, new ones added.

```yaml
services:
  el:
    env:
      RUST_LOG: debug
      MY_VAR: hello
```

### `ports`

Overrides specific named ports. Port names are defined by each service.

```yaml
services:
  el:
    ports:
      http: 9545
      authrpc: 8552
```

### Template expressions

Two template expressions are available in `args` values and are resolved at startup.

**`{{Port "name" defaultPort}}`** — declares a named port and resolves to the assigned port number. The playground allocates host ports dynamically, so this ensures no conflicts.

```yaml
services:
  myservice:
    args:
      - --http.port
      - '{{Port "http" 8545}}'
      - --metrics
      - '0.0.0.0:{{Port "metrics" 9090}}'
```

**`{{Service "name" "port" "protocol" "user"}}`** — resolves to a URL pointing at another service's port. The playground picks the right address automatically depending on where both services run:

| caller → target | resolves to |
|---|---|
| Docker → Docker | `http://service-name:internalPort` |
| Docker → host | `http://host.docker.internal:hostPort` |
| host → any | `http://localhost:hostPort` |

```yaml
services:
  myservice:
    args:
      - --el-url
      - '{{Service "el" "authrpc" "http" ""}}'
      - --beacon-url
      - '{{Service "beacon" "http" "http" ""}}'
      - --ws-url
      - '{{Service "rollup-boost" "flashblocks" "ws" ""}}'
```

`{{PortUDP "name" defaultPort}}` works the same as `Port` but registers a UDP port.

> These expressions are only meaningful inside `args`. They have no effect in other fields like `env` or `files`.

### `files`

Maps files into the container. Two source formats:

- `artifact:<name>` — a file generated at runtime by the playground (e.g. genesis, JWT)
- relative path — resolved against the recipe file's directory

```yaml
services:
  el:
    files:
      /config/genesis.json: artifact:genesis.json
      /config/config.toml: config.toml   # ./config.toml next to the recipe file
```

### `volumes`

```yaml
services:
  el:
    volumes:
      /data:
        name: el-data
        is_local: false   # false = named Docker volume, true = bind mount from playground session dir
```

To share a volume with another service, prefix the name with `shared:`. Without it, the volume name is scoped to the service (e.g. `volume-rbuilder-el-data`). With `shared:`, the service prefix is dropped, so `shared:el-data` resolves to `volume-el-data` — the same volume reth uses for its own `el-data`. This is how rbuilder accesses reth's database directly:

```yaml
services:
  rbuilder:
    volumes:
      /data_reth:
        name: "shared:el-data"
        is_local: true
```

### `depends_on`

Supported formats:
- `service-name` — wait for healthy (default)
- `service-name:healthy` — explicit healthy
- `service-name:running` — wait for running only
- `component.service:condition` — qualify with component name (validates the component exists; only the service name is used for the dependency)

Unknown conditions fall back to `healthy`.

```yaml
services:
  myservice:
    depends_on:
      - cl:healthy
      - el:running
      - beacon.cl   # same as cl:healthy when component is "beacon"
```

---

## Host execution

By default services run in Docker. Three modes let you run things on the host instead.

### `host_path`

Runs a local binary directly. Relative paths are resolved against the recipe file's directory.

```yaml
services:
  builder:
    host_path: ./bin/rbuilder   # or /absolute/path/to/binary
    args:
      - config.toml
    ready_check: http://localhost:8645/health
```

### `release`

Downloads a binary from a GitHub release and runs it on the host.

```yaml
services:
  builder:
    release:
      name: rbuilder          # asset filename prefix
      org: flashbots
      repo: rbuilder          # defaults to name if omitted
      version: v0.1.0
      format: tar.gz          # tar.gz (default) | binary | binary-arch
```

- `format: tar.gz` — downloads and extracts `<name>-<version>-<arch>.tar.gz`; arch string is inferred from the OS/arch.
- `format: binary` — downloads the raw binary at `<name>`; no extraction, arch ignored.
- `format: binary-arch` — downloads the raw binary at `<name>-<version>-<arch>`; used by releases that publish per-architecture binaries without tarballs (e.g. flashbots/rbuilder).

### Lifecycle hooks

Use this when defining a custom service with distinct setup and teardown steps — database migrations, multi-step startup sequences, anything that needs state before it can run.

```yaml
services:
  myservice:
    lifecycle_hooks: true
    init:
      - ./setup-db.sh       # run sequentially before start; each must exit 0
    start: ./run-server.sh  # long-running command (or exits 0 on success)
    stop:
      - ./shutdown.sh       # best-effort; non-zero exit is tolerated
```

`lifecycle_hooks: true` implies host execution. It cannot be combined with `host_path` or `release`.

Lifecycle services start **after** all services have passed their health checks — so there is no need to declare `depends_on` in custom services like these.

Dependencies between two lifecycle services are not supported either.

### `ready_check`

Polls a URL until it returns HTTP 200. Works for both Docker and host services.

```yaml
services:
  builder:
    ready_check: http://localhost:8645/health
```

---

## Full example

Adding rbuilder as a container to the `l1` base. Reth is already included — no need to touch it unless you want a different version.

```yaml
base: l1
description: l1 with rbuilder running as a container

recipe:
  builder:
    services:
      rbuilder:
        image: ghcr.io/flashbots/rbuilder
        tag: sha-7efdc0b
        args:
          - run
          - /data/rbuilder.toml
        files:
          /data/rbuilder.toml: rbuilder.toml        # local file next to this recipe
          /data/genesis.json: artifact:genesis.json  # generated at runtime
        volumes:
          /data_reth:
            name: "shared:el-data"   # taps into reth's volume directly
            is_local: true
        depends_on:
          - el:healthy
          - beacon:healthy
```

You'll also need a `rbuilder.toml` alongside the recipe. Generate a working starting point with:

```bash
builder-playground generate rbuilder/container
```
