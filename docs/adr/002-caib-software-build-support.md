# ADR-002: Extend caib CLI to support SoftwareBuild workflows

## Status

Proposed

## Date

2026-04-10

## Context

The `caib` CLI (`cmd/caib/`) is the developer-facing tool for the
automotive-dev-operator. Today it is structured around **OS image builds**:

```
caib
├── image       # ImageBuild: create, list, logs, download, flash
├── container   # ContainerBuild: build, logs
├── catalog     # Manage build catalogs
├── workspace   # Manage workspaces
├── auth        # OIDC auth management
└── login       # Save server endpoint
```

Every `image` subcommand flows through the **Build API** (`internal/buildapi/`),
a REST server backed by `gin` that translates HTTP requests into `ImageBuild`
CRs. The Build API's `BuildRequest` type is tightly coupled to AIB
(Automotive Image Builder) parameters: `Manifest`, `Distro`, `Target`,
`Architecture`, `ExportFormat`, `Mode`, `AutomotiveImageBuilder`,
`CustomDefs`, `AIBExtraArgs`.

PR #199 introduced the `SoftwareBuild` CRD for generic software builds
(Zephyr RTOS, Eclipse OpenBSW, or any toolchain in a container). The
`body-ecu` project demonstrated this with Zephyr firmware targeting
`native_sim` and `nucleo_h755zi_q`. Currently, `SoftwareBuild` CRs can only
be created via `kubectl apply` — there is no `caib` integration.

## Decision

### 1. Add a `caib software` subcommand tree

A new top-level subcommand `software` will mirror the `image` pattern:

```
caib
├── image       # (unchanged) OS image builds via Build API
├── software    # NEW: generic software builds via Kubernetes client
│   ├── build   # Create a SoftwareBuild CR
│   ├── list    # List SoftwareBuild CRs
│   ├── logs    # Stream build logs
│   ├── status  # Show build status and artifact references
│   └── delete  # Delete a SoftwareBuild CR
├── container   # (unchanged)
├── catalog     # (unchanged)
├── workspace   # (unchanged)
├── auth        # (unchanged)
└── login       # (unchanged)
```

### 2. Use direct Kubernetes client, not the Build API

**Chosen:** `caib software` uses `controller-runtime/client` to create and
watch `SoftwareBuild` CRs directly against the Kubernetes API.

**Rejected alternative:** Extend the Build API REST server with
`/api/v1/software-builds` endpoints.

Rationale:

- The Build API (`internal/buildapi/`) is an HTTP translation layer between
  `caib image` and `ImageBuild` CRs. Its types, auth flow, and upload
  mechanisms are deeply AIB-specific. Extending it for `SoftwareBuild` would
  require a parallel set of endpoints with entirely different request/response
  schemas, adding complexity without value.
- `SoftwareBuild` CRs are simpler than `ImageBuild` — no manifest upload, no
  distro/target matrix, no builder image management. A direct Kubernetes
  client is sufficient.
- This also aligns with the long-term direction of reducing the Build API
  surface (the existing `container` subcommand already bypasses it).
- Users who prefer `kubectl apply` continue to work unchanged.

The Kubernetes client will use the same kubeconfig / in-cluster config as
other Kubernetes-native tools. The `caib login` flow can be extended to store
a kubeconfig context alongside the Build API server URL.

### 3. Support both declarative (YAML) and imperative (flags) input

```bash
# Declarative: apply a SoftwareBuild CR from a file or Kustomize overlay
caib software build -f softwarebuild-native-sim.yaml --wait --follow
caib software build -k overlays/local/ --wait --follow

# Imperative: specify build parameters as flags
caib software build \
  --name body-ecu-native-sim \
  --source-git https://github.com/vtz/body-ecu \
  --revision main \
  --image ghcr.io/zephyrproject-rtos/ci-base:v0.27.4 \
  --fetch "west init -l src && west update" \
  --build "west build -b native_sim/native/64 src/app --pristine" \
  --deploy "cp build/zephyr/zephyr.elf /workspace/artifacts/" \
  --wait --follow
```

The declarative path reads a `SoftwareBuild` YAML file (or Kustomize overlay
directory), applies namespace defaults, and creates the CR. The imperative
path constructs a `SoftwareBuild` CR from flags. Both converge to the same
code path: create CR → watch status → stream logs.

### 4. Unified log streaming

`caib software logs <build-name>` will:

1. Resolve the `SoftwareBuild` CR to its backing `PipelineRun` via
   `status.pipelineRunName`.
2. Watch `PipelineRun.Status.ChildReferences` to discover `TaskRun` pods.
3. Stream container logs from each pod in stage order, with stage headers.

This reuses the same `logstream` package (`cmd/caib/logstream/`) that `caib
image logs` uses, adapted for the `SoftwareBuild` status structure.

### 5. Status output aligned with existing conventions

```
$ caib software status body-ecu-native-sim
Name:       body-ecu-native-sim
Phase:      Succeeded
Duration:   2m34s
Source:     git: https://github.com/vtz/body-ecu@main (abc1234)
Image:      ghcr.io/zephyrproject-rtos/ci-base@sha256:def567...
Stages:
  fetch      Succeeded  (42s)
  build      Succeeded  (1m48s)
  deploy     Succeeded  (4s)
Artifact:
  Path:       /workspace/artifacts/zephyr.elf
  Digest:     sha256:789abc...
  SBOM:       quay.io/org/body-ecu:sbom-v1.0
  Signature:  quay.io/org/body-ecu:sig-v1.0
```

The `Artifact` section is populated once ADR-001 (TSSF integration) is
implemented. Until then, only `Path` is shown.

### 6. Package structure

```
cmd/caib/
├── software/              # NEW
│   ├── software.go        # NewSoftwareCmd() — parent command
│   ├── build.go           # caib software build
│   ├── list.go            # caib software list
│   ├── logs.go            # caib software logs
│   ├── status.go          # caib software status
│   └── delete.go          # caib software delete
├── root.go                # Add software.NewSoftwareCmd() to rootCmd
└── ...
```

The new package depends on:

- `api/v1alpha1` — `SoftwareBuild` types.
- `sigs.k8s.io/controller-runtime/pkg/client` — Kubernetes CRUD.
- `cmd/caib/logstream` — shared log streaming.
- `cmd/caib/common` — shared formatting and error handling.

It does **not** depend on `internal/buildapi/`.

### 7. Kustomize support for environment overlays

The `-k` / `--kustomize` flag accepts a directory containing a
`kustomization.yaml`. This enables the pattern established in the
`automotive-builds` repository:

```
body-ecu/
├── base/
│   ├── kustomization.yaml
│   ├── softwarebuild-native-sim.yaml
│   └── softwarebuild-nucleo.yaml
└── overlays/
    ├── local/
    │   └── kustomization.yaml      # patches for local dev (PVC source, local image)
    └── ci/
        └── kustomization.yaml      # patches for CI (git source, registry image)
```

```bash
caib software build -k body-ecu/overlays/local/ --wait --follow
```

The CLI renders the overlay using `kubectl kustomize` (or the Go kustomize
library) and creates the resulting `SoftwareBuild` CRs.

## Consequences

### Positive

- Developers get a first-class CLI for `SoftwareBuild` without learning
  `kubectl`.
- The same Kustomize-based workflow works for local dev and CI.
- No changes to the Build API server — no risk of breaking `ImageBuild`.
- Log streaming and status reporting match the `caib image` experience.
- Foundation for future unification (if a single `Build` CRD is adopted per
  ADR-001, `caib build` can subsume both `image` and `software`).

### Negative

- Two auth paths: `caib image` uses Build API token, `caib software` uses
  kubeconfig. Can be unified later via `caib login` storing both.
- Adding a top-level subcommand changes the CLI surface area. Mitigated by
  following existing conventions exactly.

### Risks

- `SoftwareBuild` CRD is still in review (PR #199). The CLI work should start
  after the CRD API stabilises. Types can be imported from a feature branch
  during development.
- If a unified `Build` CRD is adopted in the future, `caib software` becomes
  `caib build --strategy software`. The migration path is an alias + deprecation.

## Implementation order

1. **Scaffold `cmd/caib/software/`** — parent command, wire into `root.go`.
2. **`caib software build -f`** — declarative CR creation from YAML.
3. **`caib software build` (flags)** — imperative CR construction.
4. **`caib software list`** — tabular output matching `caib image list`.
5. **`caib software logs`** — adapt `logstream` for PipelineRun→TaskRun
   resolution.
6. **`caib software status`** — detailed single-build view.
7. **`caib software build -k`** — Kustomize overlay support.
8. **`caib software delete`** — CR cleanup.

## References

- PR #199: feat: add SoftwareBuild CRD for multi-OS software builds
- ADR-001: Integrate Red Hat Trusted Software Supply Chain
- `automotive-builds` repository: Kustomize-based CR management for body-ecu
- [Cobra CLI framework](https://github.com/spf13/cobra)
- Existing issues: #157 (noun-verb vs verb-noun), #158 (list vs get
  inconsistency) — the new subcommand should follow whichever convention is
  adopted from these
