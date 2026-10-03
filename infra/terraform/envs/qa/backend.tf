terraform {
  backend "s3" {
    # Partial on purpose: README.md step 2 passes these at `terraform init`.
    # The QA account has its own state bucket and lock table (bootstrap/ run
    # once in that account); the key keeps QA state apart from anything else
    # ever stored there.
    #
    # bucket         = "atpost-tfstate-<qa-account-id>"
    # dynamodb_table = "atpost-tfstate-locks"
    # key            = "envs/qa/terraform.tfstate"
    # region         = "ap-south-1"
    # encrypt        = true
  }
}
