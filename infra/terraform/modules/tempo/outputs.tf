output "otlp_grpc_endpoint" {
  value       = "tempo.${var.namespace}.svc.cluster.local:4317"
  description = "OTLP gRPC ingestion endpoint. Set as OTEL_EXPORTER_OTLP_ENDPOINT (with http:// scheme stripped for gRPC) on every service."
}

output "otlp_http_endpoint" {
  value = "http://tempo.${var.namespace}.svc.cluster.local:4318"
}

output "bucket_name" {
  value = aws_s3_bucket.tempo.id
}

output "iam_role_arn" {
  value = aws_iam_role.tempo.arn
}
