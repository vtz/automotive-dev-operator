# ADR-002: SoftwareBuild CLI and Build API Extension

## Status

Accepted

## Date

2026-04-11

## Context

The `SoftwareBuild` CRD (PR #199) supports building firmware and software for
any OS or toolchain — Zephyr MCU firmware, FreeRTOS, AUTOSAR Classic, plain
CMake projects — via a five-stage Tekton pipeline. Today, the only interface
is `kubectl apply -f <softwarebuild.yaml>`, which requires developers to
author YAML and interact directly with Kubernetes. This creates three problems:

1. **Onboarding friction.** New developers must learn CRD schemas, kubectl,
   and Tekton concepts before they can build their first firmware binary.

2. **Inconsistency with ImageBuild.** OS image builds have a polished CLI
   (`caib image build`) and REST API (`/v1/builds`). SoftwareBuild has
   neither, making the platform feel incomplete when demonstrating non-RHIVOS
   workloads to customers.

3. **No local development path.** Developers iterating on firmware need a
   fast edit-compile-test loop using their local checkout and container
   images, without pushing to Git every time.

Meanwhile, compliance frameworks (TSSF, ISO 26262, ISO/SAE 21434, UNECE R155)
require that official build definitions are version-controlled, reviewed, and
auditable. A purely imperative CLI that creates ad-hoc CRs would break this
chain.

### Prior art in this codebase

- **`caib image build`** (`cmd/caib/buildcmd/`): imperative CLI that creates
  `ImageBuild` CRs via the Build API. The developer provides a manifest and
  flags; the API creates the CR. Log streaming, progress, and download are
  built-in.

- **`hack/setup-cluster.sh --local`** (`feat/local-dev-scripts`): mounts a
  host workspace into Kind, creates a PVC, and copies files with a busybox
  populator pod. Proves the local build pattern.

- **`hack/run-pipeline.sh`** (`feat/local-dev-scripts`): applies a
  SoftwareBuild CR (file or Kustomize overlay), runs the operator locally,
  waits for PipelineRun completion, and shows results.

- **`automotive-builds/body-ecu/overlays/local`**: Kustomize overlay that
  patches SoftwareBuild CRs to use PVC source and local container images
  instead of Git. Proves the overlay-based local development pattern.

- **ADR-001** (`feat/tssf-integration`): TSSF integration with Tekton Chains,
  SBOM generation, and Enterprise Contract. Defines `ComplianceConfig` on
  `OperatorConfig` and the compliance pipeline tail.

## Decision

### 1. Dual-mode CLI with role separation

Add a `caib software` command group that supports two modes of operation:

**Developer mode** — trigger, monitor, and inspect builds via the Build API:
- `caib software list` — list available SoftwareBuild configurations
- `caib software run <name>` — trigger a build from an existing CR
- `caib software run <name> --local` — build using local workspace and container
- `caib software show <name>` — display build status and compliance info
- `caib software logs <name>` — stream build logs
- `caib software delete <name>` — delete a build

**SRE/build engineer mode** — author and manage build definitions:
- `caib software create --dry-run -o yaml` — generate CR YAML from flags
- `caib software validate -f <file>` — validate CR schema before applying
- `caib software apply -f <file|dir>` — apply CR with validation

The developer never creates or modifies CRs. They consume build
configurations that were authored by a build engineer, reviewed in a PR,
and applied by the SRE or GitOps.

### 2. Three build tiers with tiered compliance

| Tier | Trigger | Source | Compliance |
|------|---------|--------|------------|
| **Local** | `caib software run --local` | Working directory (uncommitted OK) | Skipped by default |
| **Remote** | `caib software run <name>` | Git (from CR, revision overridable) | Per OperatorConfig |
| **Official** | GitOps / `caib software apply` | Git (committed, reviewed CR) | Mandatory |

Local builds produce **transient** PipelineRuns that are developer
experiments, not auditable artifacts. They do not modify the SoftwareBuild
CR status.

Official builds are triggered by applying reviewed CRs via GitOps.
`OperatorConfig.spec.compliance.enabled: true` enforces SBOM, signing,
and provenance. Builds that skip compliance fail Enterprise Contract policy.

### 3. Build API: `/v1/software-builds` endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/v1/software-builds` | List SoftwareBuild CRs |
| GET | `/v1/software-builds/:name` | Get status + spec summary |
| POST | `/v1/software-builds/:name/run` | Start build (revision/local override) |
| GET | `/v1/software-builds/:name/logs` | Stream PipelineRun logs |
| DELETE | `/v1/software-builds/:name` | Delete CR |

The API does **not** expose `POST /v1/software-builds` to create CRs from
JSON. This is intentional: build definitions must come from reviewed Git
sources, not ad-hoc API calls.

The `run` endpoint accepts optional overrides:
- `revision` — build a different branch (remote mode)
- `localWorkspace` — reference a pre-synced PVC (local mode)
- `image` — override runtime container image
- `skipCompliance` — skip SBOM/signing (rejected when enforcement is active)

### 4. Local mode mechanics

When `--local` is used, the CLI:

1. Syncs the local workspace directory to a PVC on the cluster (reusing the
   workspace sync API or the PVC populator pattern from `setup-cluster.sh`).
2. Optionally loads a local container image into the cluster (Kind or
   internal registry).
3. Creates a transient PipelineRun with the PVC workspace and local image,
   mirroring the `overlays/local` Kustomize pattern.
4. Streams logs and reports results.
5. Does not modify the SoftwareBuild CR itself.

## Alternatives Considered

### Alternative 1: Full imperative CLI

`caib software build --source ... --build ...` would create SoftwareBuild
CRs on the fly from CLI flags, similar to how `caib image build` works.

**Pros:** Simplest UX. One command from zero to build.

**Cons:**
- Build definitions are ephemeral — no Git trail, no review.
- Violates four-eyes principle required by ISO 26262 Part 8.
- Auditors cannot trace "what built this firmware."
- SLSA L3 requires build definitions from version-controlled sources.

**Verdict:** Rejected for production/official builds. The pattern is preserved
as `caib software create --dry-run` for experimentation, and the generated
YAML is the "graduation" path to a reviewed CR.

### Alternative 2: kubectl-only

Developers use `kubectl apply -f` and `kubectl get softwarebuilds`.

**Pros:** No additional code.

**Cons:**
- Poor developer UX: YAML authoring, pod name hunting for logs.
- No log streaming abstraction.
- No revision override without modifying the CR.
- Inconsistent with the `caib image` experience.
- No local build mode.

**Verdict:** Rejected.

### Alternative 3: Always-remote with mandatory compliance

All builds run on the cluster with full compliance, no local mode.

**Pros:** Every artifact is signed and attested.

**Cons:**
- Developer inner loop is too slow: every edit requires git push, cluster
  build, SBOM, signing.
- Mirrors no real automotive OEM workflow — developers always build locally
  first. Only release builds go through the full compliance pipeline.

**Verdict:** Rejected.

### Alternative 4: Dual-mode with tiered compliance (chosen)

Combines fast local iteration with auditable official builds. The
`OperatorConfig.spec.compliance.enabled` flag enforces compliance for all
non-local PipelineRuns. Local builds are explicitly out of scope for
certification evidence.

## Compliance Mapping

### ISO 26262 Part 8

- **Clause 7 (Configuration Management):** SoftwareBuild CRs stored in Git
  are versioned build definitions. Changes go through PR review (four-eyes
  principle). Local builds are developer tools, not configuration items.

- **Clause 9 (Reproducibility):** CR + pinned container image tag =
  reproducible build. Local builds with mutable tags (e.g.,
  `localhost/dev:latest`) are intentionally not reproducible — they are
  experiments.

### ISO/SAE 21434

- **RQ-06-01 (Secure Development Lifecycle):** Build definitions are part
  of the cybersecurity assurance case. Only official builds (from reviewed
  CRs) produce admissible evidence.

### SLSA v1.0

- **L3 (Tamper-evident):** CR from reviewed Git repo, applied via GitOps,
  built with TaskRef (not inline TaskSpec, per issue #200), signed by
  Tekton Chains. Local builds intentionally do not produce SLSA attestations.

### UNECE R155

- **Supply chain:** SBOM + provenance via TSSF (ADR-001). Enforced at the
  `OperatorConfig` level for official builds.

### Compliance skip justification

Skipping compliance for local builds is not a gap. It mirrors how every
automotive OEM operates today: developers build and test locally without
signing every debug binary. Only release builds go through the full
compliance pipeline. The platform enforces the boundary — developers cannot
produce "official" artifacts without compliance.

## Role Separation

| Role | Responsibilities | CLI Commands |
|------|------------------|--------------|
| Platform SRE | OperatorConfig, RBAC, cluster infra | `caib software apply` |
| Build engineer | SoftwareBuild CRs, Kustomize overlays | `caib software create`, `validate`, `apply` |
| Developer | Trigger builds, monitor, download | `caib software run`, `list`, `show`, `logs` |
| CI system | Apply reviewed CRs, enforce compliance | `caib software apply -f overlays/prod` |

### RBAC

- **Developer:** `get`, `list`, `watch` on `softwarebuilds`; build
  triggering is mediated by the Build API (no direct CR mutation needed).
- **Build engineer:** full CRUD on `softwarebuilds`.
- **CI service account:** `get`, `list`, `create`, `update` on
  `softwarebuilds` (applies CRs from Git).

## Consequences

### Positive

- Developers onboard in minutes: `caib software list` → `caib software run`.
- Local iteration is fast: no git push, no compliance overhead.
- Official builds are auditable: CR in Git, compliance enforced.
- Consistent UX across ImageBuild and SoftwareBuild.
- Platform demonstrates value for both RHIVOS and non-RHIVOS workloads.

### Negative

- Build API server grows by ~300 lines (handlers, types, route registration).
- CLI grows by ~500 lines (8 subcommands, handler logic, local mode helpers).
- Local mode adds complexity: workspace sync, image loading, transient runs.

### Risks

- Local mode workspace sync may be slow for large repositories. Mitigated
  by incremental sync (existing `/v1/workspaces` sync API) and developer
  control over what is synced.
- The `run` endpoint's revision override could be abused to build arbitrary
  code. Mitigated by RBAC and the `requested-by` annotation for audit.

## Future Considerations

- `caib software download` when artifact storage moves beyond shared PVC
  (OCI artifact push for firmware binaries).
- Compliance status display in `caib software show` once TSSF lands for
  SoftwareBuild (issue #200 prerequisite).
- `caib software promote` for GitOps-based overlay promotion
  (dev → staging → prod).
- Workspace file-watch mode for continuous local builds.

## References

- PR #199: feat: add SoftwareBuild CRD for multi-OS software builds
- ADR-001: Integrate Red Hat Trusted Software Supply Chain (RHTSSC)
- Issue #200: Migrate SoftwareBuild pipeline from inline TaskSpec to signed
  TaskRef bundles
- `automotive-builds/body-ecu/` — example Kustomize manifests for Zephyr
  MCU builds
