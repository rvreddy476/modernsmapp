output "topics" {
  value       = sort(var.topics)
  description = "Topics the Job creates (sorted)."
}

output "topic_count" {
  value = length(var.topics)
}
