# ScyllaDB backups (feed-12): the bucket Scylla Manager writes to and the IAM
# role the Scylla pods' manager agent assumes. The backup TASK itself (daily,
# all keyspaces) is in modules/scylla/backup.tf.
#
# RETENTION, stated once:
#   - Scylla Manager keeps the last var.scylla_backup_retention daily backups
#     (7 here = 7 days) and deletes older ones ITSELF (it purges the
#     SSTable files no kept backup refers to).
#   - The bucket has NO age-based expiry of current objects, on purpose: a
#     backup is incremental, so a new backup re-uses SSTable files uploaded
#     days or weeks ago. An S3 rule that deleted objects by age would silently
#     break every backup that still refers to them.
#   - Versioning is on and noncurrent versions are kept 14 days, so a file
#     Scylla Manager (or a person) deleted can be brought back for 14 days.
#   - Incomplete multipart uploads are aborted after 7 days (the module
#     default).
#
# NOT applied and NOT tested: no backup has run and no restore has been
# drilled. docs/runbooks/restore-scylla.md is the drill.

module "bucket_scylla_backups" {
  source = "../../modules/private-bucket"

  environment                        = var.environment
  purpose                            = "scylla-backups"
  service_tag                        = "scylla"
  versioning                         = true
  noncurrent_version_expiration_days = 14
}

# The Scylla Manager agent runs as a sidecar in every Scylla pod, under the
# ServiceAccount the operator creates for the cluster: <cluster name>-member
# in namespace scylla. The role can read, write and delete objects in the
# backup bucket and nothing else (the bucket module's client policy).
module "scylla_backup_irsa" {
  source = "../../modules/service-irsa"

  environment         = var.environment
  service_name        = "scylla-backup"
  oidc_provider_arn   = module.eks.oidc_provider_arn
  oidc_provider_url   = module.eks.oidc_provider_url
  k8s_namespace       = "scylla"
  k8s_service_account = "atpost-${var.environment}-member"
  policy_arns         = [] # attached below: the ARN is not known at plan time and the module keys a for_each on it
}

resource "aws_iam_role_policy_attachment" "scylla_backup_bucket" {
  role       = module.scylla_backup_irsa.role_name
  policy_arn = module.bucket_scylla_backups.client_iam_policy_arn
}

variable "scylla_backup_retention" {
  description = "How many daily Scylla backups Scylla Manager keeps (one per day, so this is days)."
  type        = number
  default     = 7
}

output "scylla_backups_bucket_name" { value = module.bucket_scylla_backups.bucket_name }
output "scylla_backup_irsa_role_arn" { value = module.scylla_backup_irsa.role_arn }
