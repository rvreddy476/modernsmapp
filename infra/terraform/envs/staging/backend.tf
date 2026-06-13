terraform {
  backend "s3" {
    # Set to the bootstrap module's outputs. Concrete values committed
    # so a fresh checkout's `terraform init` finds the state — these
    # don't leak any secrets (just bucket + key + region).
    #
    # bucket         = "atpost-tfstate-<account-id>"   # TODO: fill in after bootstrap
    # dynamodb_table = "atpost-tfstate-locks"
    # key            = "envs/staging/terraform.tfstate"
    # region         = "ap-south-1"
    # encrypt        = true
  }
}
