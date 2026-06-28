output "namespace" {
  value = kubernetes_namespace.ingress_nginx.metadata[0].name
}

output "ingress_class_name" {
  value = "nginx"
}
