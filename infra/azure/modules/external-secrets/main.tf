# External Secrets Operator — Azure twin of the AWS external-secrets
# module. Bridges Azure Key Vault into kubernetes Secrets so app pods
# consume plain secretKeyRef references (identical to AWS). The
# per-service ExternalSecret resources in the chart are reused verbatim;
# only the ClusterSecretStore name (azure-key-vault) and the KV secret
# name differ in the -azure values.
#
# Auth: Entra Workload Identity (the IRSA equivalent). A user-assigned
# managed identity is federated to the external-secrets/external-secrets
# ServiceAccount via the AKS OIDC issuer, and granted Key Vault Secrets
# User (read-only) on the vault. No static credentials anywhere.

# Managed identity ESO authenticates as.
resource "azurerm_user_assigned_identity" "eso" {
  name                = "atpost-${var.environment}-external-secrets"
  resource_group_name = var.resource_group_name
  location            = var.location

  tags = {
    environment = var.environment
    managed-by  = "terraform"
  }
}

# Federate it to the ESO ServiceAccount — the AKS OIDC issuer mints a
# token for system:serviceaccount:external-secrets:external-secrets,
# which Entra exchanges for this identity.
resource "azurerm_federated_identity_credential" "eso" {
  name                = "external-secrets"
  resource_group_name = var.resource_group_name
  parent_id           = azurerm_user_assigned_identity.eso.id
  issuer              = var.aks_oidc_issuer_url
  subject             = "system:serviceaccount:external-secrets:external-secrets"
  audience            = ["api://AzureADTokenExchange"]
}

# Read-only access to the whole vault. The AWS module scopes by secret
# prefix; on Azure the vault is already per-env, so vault-level read is
# the equivalent blast radius.
resource "azurerm_role_assignment" "eso_kv_read" {
  scope                = var.key_vault_id
  role_definition_name = "Key Vault Secrets User"
  principal_id         = azurerm_user_assigned_identity.eso.principal_id
}

resource "kubernetes_namespace" "external_secrets" {
  metadata {
    name = "external-secrets"
    labels = {
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }
}

# ESO Helm chart. Pin the version (CRD renames between minors). The
# ServiceAccount is annotated with the identity client-id and labelled
# for Workload Identity; the controller pods get the use-label so the
# webhook injects the projected token.
resource "helm_release" "external_secrets" {
  name       = "external-secrets"
  repository = "https://charts.external-secrets.io"
  chart      = "external-secrets"
  version    = var.chart_version
  namespace  = kubernetes_namespace.external_secrets.metadata[0].name

  set {
    name  = "installCRDs"
    value = "true"
  }

  # Workload Identity wiring on the ServiceAccount.
  set {
    name  = "serviceAccount.annotations.azure\\.workload\\.identity/client-id"
    value = azurerm_user_assigned_identity.eso.client_id
  }
  set {
    name  = "serviceAccount.labels.azure\\.workload\\.identity/use"
    value = "true"
  }
  # Pod label so the mutating webhook projects the federated token.
  set {
    name  = "podLabels.azure\\.workload\\.identity/use"
    value = "true"
  }

  # Platform tooling — keep it on the system node pool.
  set {
    name  = "nodeSelector.workload"
    value = "system"
  }
  set {
    name  = "webhook.nodeSelector.workload"
    value = "system"
  }
  set {
    name  = "certController.nodeSelector.workload"
    value = "system"
  }

  set {
    name  = "replicaCount"
    value = "2"
  }

  depends_on = [azurerm_role_assignment.eso_kv_read]
}

# ClusterSecretStore — the cluster-wide pointer at Key Vault. Service
# ExternalSecrets reference it by name (azure-key-vault). The Azure
# provider supports `property` extraction from a JSON secret, so the
# per-service data: [{secretKey, remoteRef}] maps 1:1 with the AWS form.
resource "kubernetes_manifest" "secret_store" {
  manifest = {
    apiVersion = "external-secrets.io/v1beta1"
    kind       = "ClusterSecretStore"
    metadata = {
      name = "azure-key-vault"
    }
    spec = {
      provider = {
        azurekv = {
          authType = "WorkloadIdentity"
          vaultUrl = var.key_vault_uri
          serviceAccountRef = {
            name      = "external-secrets"
            namespace = "external-secrets"
          }
        }
      }
    }
  }

  depends_on = [helm_release.external_secrets]
}
