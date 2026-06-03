terraform {
  backend "s3" {
    # See envs/staging/backend.tf — same bucket, different key.
    #
    # bucket         = "atpost-tfstate-<account-id>"
    # dynamodb_table = "atpost-tfstate-locks"
    # key            = "envs/prod/terraform.tfstate"
    # region         = "ap-south-1"
    # encrypt        = true
  }
}
