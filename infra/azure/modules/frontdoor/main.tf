# Azure Front Door (Standard/Premium) + WAF — the internet edge in front
# of the in-cluster nginx ingress. AWS twin = CloudFront + regional WAFv2
# on the ALB. Front Door terminates TLS, runs the WAF, and forwards to
# the nginx LoadBalancer (origin).
#
# The origin host (var.origin_host_name) is the nginx controller's public
# IP / DNS name — only known AFTER the ingress-nginx Service gets its
# Azure LB IP, so it's an operator-supplied value (set it once the LB is
# up; see docs/DEPLOY-azure.md). WAF managed rule sets require the
# Premium_AzureFrontDoor SKU.

resource "azurerm_cdn_frontdoor_profile" "this" {
  name                = "atpost-${var.environment}-fd"
  resource_group_name = var.resource_group_name
  sku_name            = var.sku_name

  tags = {
    environment = var.environment
    managed-by  = "terraform"
  }
}

resource "azurerm_cdn_frontdoor_endpoint" "this" {
  name                     = "atpost-${var.environment}"
  cdn_frontdoor_profile_id = azurerm_cdn_frontdoor_profile.this.id
}

resource "azurerm_cdn_frontdoor_origin_group" "nginx" {
  name                     = "nginx-ingress"
  cdn_frontdoor_profile_id = azurerm_cdn_frontdoor_profile.this.id

  load_balancing {
    sample_size                 = 4
    successful_samples_required = 3
  }

  health_probe {
    path                = "/healthz"
    protocol            = "Https"
    request_type        = "GET"
    interval_in_seconds = 30
  }
}

resource "azurerm_cdn_frontdoor_origin" "nginx" {
  name                          = "nginx-origin"
  cdn_frontdoor_origin_group_id = azurerm_cdn_frontdoor_origin_group.nginx.id
  enabled                       = true

  host_name          = var.origin_host_name
  origin_host_header = var.origin_host_header != "" ? var.origin_host_header : var.origin_host_name
  http_port          = 80
  https_port         = 443
  priority           = 1
  weight             = 1000

  # nginx serves the Azure LB cert / Front Door-managed cert; skip strict
  # origin cert name checks (TLS to the origin still happens).
  certificate_name_check_enabled = false
}

resource "azurerm_cdn_frontdoor_route" "default" {
  name                          = "default-route"
  cdn_frontdoor_endpoint_id     = azurerm_cdn_frontdoor_endpoint.this.id
  cdn_frontdoor_origin_group_id = azurerm_cdn_frontdoor_origin_group.nginx.id
  cdn_frontdoor_origin_ids      = [azurerm_cdn_frontdoor_origin.nginx.id]

  supported_protocols    = ["Http", "Https"]
  patterns_to_match      = ["/*"]
  forwarding_protocol    = "HttpsOnly"
  https_redirect_enabled = true
  link_to_default_domain = true
}

# ── WAF ──────────────────────────────────────────────────────────
resource "azurerm_cdn_frontdoor_firewall_policy" "this" {
  name                = "atpost${var.environment}waf"
  resource_group_name = var.resource_group_name
  sku_name            = var.sku_name
  enabled             = true
  mode                = var.waf_mode

  # Blunt per-IP rate limiter (credential-stuffing / DoS brake), AWS twin
  # of the rate-based WAFv2 rule.
  custom_rule {
    name     = "RateLimitPerIP"
    enabled  = true
    priority = 1
    type     = "RateLimitRule"
    action   = "Block"

    rate_limit_duration_in_minutes = 1
    rate_limit_threshold           = var.rate_limit_threshold

    match_condition {
      match_variable = "RemoteAddr"
      operator       = "IPMatch"
      match_values   = ["0.0.0.0/0"]
    }
  }

  # Managed rule sets (Premium SKU): OWASP baseline + bot manager.
  dynamic "managed_rule" {
    for_each = var.sku_name == "Premium_AzureFrontDoor" ? [1] : []
    content {
      type    = "Microsoft_DefaultRuleSet"
      version = "2.1"
      action  = "Block"
    }
  }

  dynamic "managed_rule" {
    for_each = var.sku_name == "Premium_AzureFrontDoor" ? [1] : []
    content {
      type    = "Microsoft_BotManagerRuleSet"
      version = "1.0"
      action  = "Block"
    }
  }
}

# Associate the WAF with the endpoint's default domain.
resource "azurerm_cdn_frontdoor_security_policy" "this" {
  name                     = "atpost-${var.environment}-secpol"
  cdn_frontdoor_profile_id = azurerm_cdn_frontdoor_profile.this.id

  security_policies {
    firewall {
      cdn_frontdoor_firewall_policy_id = azurerm_cdn_frontdoor_firewall_policy.this.id

      association {
        domain {
          cdn_frontdoor_domain_id = azurerm_cdn_frontdoor_endpoint.this.id
        }
        patterns_to_match = ["/*"]
      }
    }
  }
}
