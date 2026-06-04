output "service_url" {
  value       = "http://loki.${var.namespace}.svc.cluster.local:3100"
  description = "Loki push + query URL. Grafana datasource ConfigMap already points here; emitted for any non-Grafana consumer."
}

output "bucket_name" {
  value = aws_s3_bucket.loki.id
}

output "iam_role_arn" {
  value = aws_iam_role.loki.arn
}
