terraform {
  backend "azurerm" {
    resource_group_name  = "atpost-tfstate"
    storage_account_name = "atposttfstate454350"
    container_name       = "tfstate"
    key                  = "envs/prod/terraform.tfstate"
  }
}
