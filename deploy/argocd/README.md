# ArgoCD delivery

GitOps entry point for the MindMesh platform: one `AppProject` (`project.yaml`) and one
`ApplicationSet` (`applicationset.yaml`) that expands into three `Application`s — one per
environment — each pointing at [`../helm/mindmesh`](../helm/mindmesh) with the matching
`values-<env>.yaml` overlay.

## Prerequisites

- ArgoCD installed in-cluster (`argocd` namespace), with the ApplicationSet controller enabled
  (bundled in ArgoCD ≥ 2.x core install).
- For staging/prod: the [External Secrets Operator](https://external-secrets.io) plus a
  `ClusterSecretStore` named `aws-secretsmanager` (or edit `secretStoreRef` in the relevant
  `values-<env>.yaml` to match your store). See [`../README.md`](../README.md) for what's in vs.
  out of scope for secrets.
- A real Git remote for this repo (the manifests below use
  `https://github.com/mindmesh/mindmesh.git` as a placeholder — update `sourceRepos` in
  `project.yaml` and `repoURL` in `applicationset.yaml` to match).

## Bootstrap

```bash
kubectl apply -f deploy/argocd/project.yaml
kubectl apply -f deploy/argocd/applicationset.yaml
```

This creates `Application/mindmesh-dev`, `Application/mindmesh-staging`, and
`Application/mindmesh-prod`, each targeting its own namespace (`mindmesh-<env>`, auto-created via
`CreateNamespace=true`).

## Promotion model

- **dev, staging** — `automated: { prune: true, selfHeal: true }`: every push to `targetRevision`
  (default `main`) syncs automatically.
- **prod** — no `automated` block: changes are staged (visible as `OutOfSync` in the ArgoCD UI/CLI)
  but require an explicit sync:
  ```bash
  argocd app sync mindmesh-prod
  ```

## Secrets at sync time

`values-staging.yaml` / `values-prod.yaml` intentionally leave `<service>.migrations.databaseUrl`
blank (the migration Job no-ops until it's set — see the comment at the top of each overlay).
Real deployments supply it per-`Application` via `spec.source.helm.parameters`, fed by whatever
secret plugin your ArgoCD install uses (e.g.
[argocd-vault-plugin](https://argocd-vault-plugin.readthedocs.io/)) — never by committing a DSN
to a values file. A commented example is in `applicationset.yaml`.

## Out of scope (see [`../README.md`](../README.md) for the full list)

Multi-region (a `matrix`/`clusters` generator fanning each env out across regions + GeoDNS),
Argo Rollouts canary steps gated on Prometheus SLO queries, and a real secret-plugin wiring for
the migration DSNs above are not implemented here — this ApplicationSet is single-cluster,
single-region, three-environment.
