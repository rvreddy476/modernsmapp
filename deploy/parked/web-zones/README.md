# Parked: the eleven micro-frontend zones

These directories (`shell`, `admin`, `commerce`, `community`, `creator`,
`dating`, `live`, `memories`, `messenger`, `miniapps`, `social`) described the
Multi-Zone web of the `atpost-web` repository, with one image per zone
(`atpost/web-<zone>`). That is not what runs: production serves the single
`postbook-ui` app (`deploy/web/app`) and the admin console from
`atpost-web-ui/apps/admin` (`deploy/web/admin-console`).

Parked on 3 Oct 2026 (W6) rather than deleted because `scripts/gen-azure-
values.sh` and `docs/DEPLOY-azure.md` still name them. Nothing deploys them:
they live OUTSIDE `deploy/web/` on purpose. The ArgoCD git files generator
matches `deploy/web/*/values-<env>.yaml`, and with ArgoCD's default (legacy)
globbing `*` also matches `/`, so a parked copy anywhere under `deploy/web/`
(for example `deploy/web/_unused/shell/values-prod.yaml`) WOULD have become
an Application. The Terraform `web_zone_names` ECR list that mirrored them
should be replaced by `web` and `admin-console` (W1).
Delete the whole directory once the Azure scripts are retired.
