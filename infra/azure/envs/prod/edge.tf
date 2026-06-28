# Edge (Phase 3): Front Door + WAF + DNS. Disabled by default — enable
# once the ingress-nginx Service has a public LB IP (set as the origin)
# and you have a DNS zone to point at Front Door. See docs/DEPLOY-azure.md.
module "frontdoor" {
  count  = var.enable_edge ? 1 : 0
  source = "../../modules/frontdoor"

  environment         = var.environment
  resource_group_name = module.resource_group.name
  origin_host_name    = var.edge_origin_host_name
  sku_name            = var.edge_frontdoor_sku
}

module "dns" {
  count  = var.enable_edge ? 1 : 0
  source = "../../modules/dns"

  environment         = var.environment
  resource_group_name = module.resource_group.name
  zone_name           = var.edge_zone_name
  cname_records       = var.edge_cname_records
}

output "frontdoor_endpoint" {
  value = var.enable_edge ? module.frontdoor[0].endpoint_host_name : ""
}

output "dns_name_servers" {
  value = var.enable_edge ? module.dns[0].name_servers : []
}
