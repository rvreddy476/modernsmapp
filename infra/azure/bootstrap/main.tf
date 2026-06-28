# One-time bootstrap: the Azure Storage backend for Terraform state. Run this
# FIRST (local state), then fill envs/*/backend.tf with these outputs and migrate.
# Mirrors the AWS infra/terraform/bootstrap (S3 + DynamoDB) role.
terraform {
  required_version = ">= 1.6, < 2.0"
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 4.0"
    }
  }
}

provider "azurerm" {
  features {}
  subscription_id = var.subscription_id
}

variable "subscription_id" {
  type = string
}

variable "location" {
  type    = string
  default = "centralindia"
}

resource "azurerm_resource_group" "tfstate" {
  name     = "atpost-tfstate"
  location = var.location
}

resource "azurerm_storage_account" "tfstate" {
  name                            = "atposttfstate" # globally unique; adjust if taken
  resource_group_name             = azurerm_resource_group.tfstate.name
  location                        = azurerm_resource_group.tfstate.location
  account_tier                    = "Standard"
  account_replication_type        = "GRS"
  min_tls_version                 = "TLS1_2"
  allow_nested_items_to_be_public = false
  blob_properties {
    versioning_enabled = true
  }
}

resource "azurerm_storage_container" "tfstate" {
  name                  = "tfstate"
  storage_account_id    = azurerm_storage_account.tfstate.id
  container_access_type = "private"
}

output "resource_group_name" { value = azurerm_resource_group.tfstate.name }
output "storage_account_name" { value = azurerm_storage_account.tfstate.name }
output "container_name" { value = azurerm_storage_container.tfstate.name }
