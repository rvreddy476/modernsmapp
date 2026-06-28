# Azure DNS — public zone + CNAME records pointing the app hostnames at
# the Front Door endpoint. AWS twin = the Route 53 zone module. As on
# AWS, the registrar / public edge may stay external (e.g. Cloudflare);
# this zone owns the Azure-side hostnames.
#
# Cloud switch = re-point these CNAMEs (Front Door ⇄ CloudFront/ALB). The
# records map makes that a one-value change per host.

resource "azurerm_dns_zone" "this" {
  name                = var.zone_name
  resource_group_name = var.resource_group_name

  tags = {
    environment = var.environment
    managed-by  = "terraform"
  }
}

# CNAME records → Front Door endpoint. Keyed by subdomain label
# (e.g. "api", "app"); value is the CNAME target (the Front Door host).
resource "azurerm_dns_cname_record" "records" {
  for_each = var.cname_records

  name                = each.key
  zone_name           = azurerm_dns_zone.this.name
  resource_group_name = var.resource_group_name
  ttl                 = var.ttl
  record              = each.value
}
