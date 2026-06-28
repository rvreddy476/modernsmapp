terraform {
  backend "azurerm" {
    # Fill from infra/azure/bootstrap outputs, then `terraform init -migrate-state`.
    # Or pass via -backend-config in CI (see .github/workflows/terraform-azure.yml).
    # resource_group_name  = "atpost-tfstate"
    # storage_account_name = "atposttfstate"
    # container_name       = "tfstate"
    # key                  = "envs/staging/terraform.tfstate"
  }
}
