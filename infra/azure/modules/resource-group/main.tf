# Resource group — the container for all atpost-<env> Azure resources. Mirrors
# the AWS module conventions (environment var, default tags). AWS infra under
# infra/terraform/ is untouched; this infra/azure/ tree is fully additive.
resource "azurerm_resource_group" "this" {
  name     = "atpost-${var.environment}"
  location = var.location

  tags = {
    Project     = "atpost"
    Environment = var.environment
    ManagedBy   = "terraform"
  }
}
