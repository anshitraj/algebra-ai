terraform {
  required_version = ">= 1.10"
  required_providers {
    google = { source = "hashicorp/google", version = "~> 7.0" }
  }
  # Supply a private, versioned state bucket with -backend-config. No secrets
  # or service-account keys are stored in this module's variables or outputs.
  backend "gcs" {}
}

provider "google" {
  project = var.project_id
  region  = var.region
}

variable "project_id" {
  type = string
}
variable "region" {
  type    = string
  default = "asia-south1"
}
variable "domain" {
  type = string
}
variable "api_image" {
  type = string
}
variable "web_image" {
  type = string
}
variable "api_url" {
  type        = string
  description = "Stable backend run.app URI, also baked into the frontend image. Bootstrap API first."
}
variable "database_url_secret" {
  type = string
}
variable "resend_key_secret" {
  type = string
}
variable "master_ciphertext_secret" {
  type = string
}
variable "mainnet_rpc_secret" {
  type = string
}
variable "web_secrets" {
  type        = map(string)
  default     = {}
  description = "Environment name to existing Secret Manager secret ID; e.g. GEMINI_API_KEY."
}
variable "privy_app_id" {
  type    = string
  default = ""
}
variable "email_from" {
  type = string
}
variable "alert_email" {
  type = string
}
variable "program_id" {
  type    = string
  default = "F1Uu8ynQUviLZHUgejHDMkKmFAJJ7ZKDwU3Svc7Xpj21"
}

locals {
  apis = toset(["run.googleapis.com", "compute.googleapis.com", "servicenetworking.googleapis.com", "sqladmin.googleapis.com", "redis.googleapis.com", "cloudkms.googleapis.com", "secretmanager.googleapis.com", "monitoring.googleapis.com", "logging.googleapis.com"])
  api_secrets = {
    DATABASE_URL                  = var.database_url_secret
    RESEND_API_KEY                = var.resend_key_secret
    ALGEBRA_MASTER_KEY_CIPHERTEXT = var.master_ciphertext_secret
    SOLANA_MAINNET_RPC_URL        = var.mainnet_rpc_secret
  }
}

resource "google_project_service" "enabled" {
  for_each           = local.apis
  service            = each.value
  disable_on_destroy = false
}
resource "google_compute_network" "private" {
  name                    = "algebra"
  auto_create_subnetworks = false
  depends_on              = [google_project_service.enabled]
}
resource "google_compute_subnetwork" "services" {
  name                     = "algebra-services"
  ip_cidr_range            = "10.80.0.0/24"
  network                  = google_compute_network.private.id
  private_ip_google_access = true
}
resource "google_compute_router" "egress" {
  name    = "algebra-egress"
  network = google_compute_network.private.id
}
resource "google_compute_router_nat" "egress" {
  name                               = "algebra-egress"
  router                             = google_compute_router.egress.name
  nat_ip_allocate_option             = "AUTO_ONLY"
  source_subnetwork_ip_ranges_to_nat = "LIST_OF_SUBNETWORKS"
  subnetwork {
    name                    = google_compute_subnetwork.services.id
    source_ip_ranges_to_nat = ["ALL_IP_RANGES"]
  }
}
resource "google_compute_global_address" "private_services" {
  name          = "algebra-private-services"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 16
  network       = google_compute_network.private.id
}
resource "google_service_networking_connection" "private" {
  network                 = google_compute_network.private.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.private_services.name]
}
resource "google_sql_database_instance" "postgres" {
  name                = "algebra-postgres"
  database_version    = "POSTGRES_16"
  region              = var.region
  deletion_protection = true
  settings {
    tier              = "db-custom-2-7680"
    availability_type = "REGIONAL"
    disk_autoresize   = true
    ip_configuration {
      ipv4_enabled    = false
      private_network = google_compute_network.private.id
      ssl_mode        = "ENCRYPTED_ONLY"
    }
    backup_configuration {
      enabled                        = true
      point_in_time_recovery_enabled = true
      start_time                     = "20:00"
      transaction_log_retention_days = 7
      backup_retention_settings {
        retained_backups = 14
      }
    }
  }
  depends_on = [google_service_networking_connection.private]
}
resource "google_sql_database" "algebra" {
  name     = "algebra"
  instance = google_sql_database_instance.postgres.name
}
resource "google_redis_instance" "redis" {
  name               = "algebra-redis"
  tier               = "STANDARD_HA"
  memory_size_gb     = 1
  region             = var.region
  authorized_network = google_compute_network.private.id
  connect_mode       = "PRIVATE_SERVICE_ACCESS"
  redis_version      = "REDIS_7_2"
  depends_on         = [google_service_networking_connection.private]
}
resource "google_service_account" "api" {
  account_id = "algebra-api"
}
resource "google_service_account" "web" {
  account_id = "algebra-web"
}
resource "google_kms_key_ring" "keys" {
  name       = "algebra"
  location   = var.region
  depends_on = [google_project_service.enabled]
}
resource "google_kms_crypto_key" "payer" {
  name     = "solana-payer"
  key_ring = google_kms_key_ring.keys.id
  purpose  = "ASYMMETRIC_SIGN"
  version_template {
    algorithm        = "EC_SIGN_ED25519"
    protection_level = "SOFTWARE"
  }
  lifecycle {
    prevent_destroy = true
  }
}
resource "google_kms_crypto_key" "master" {
  name            = "master-key"
  key_ring        = google_kms_key_ring.keys.id
  purpose         = "ENCRYPT_DECRYPT"
  rotation_period = "7776000s"
  lifecycle {
    prevent_destroy = true
  }
}
resource "google_kms_crypto_key_iam_member" "sign" {
  crypto_key_id = google_kms_crypto_key.payer.id
  role          = "roles/cloudkms.signerVerifier"
  member        = "serviceAccount:${google_service_account.api.email}"
}
resource "google_kms_crypto_key_iam_member" "decrypt" {
  crypto_key_id = google_kms_crypto_key.master.id
  role          = "roles/cloudkms.cryptoKeyDecrypter"
  member        = "serviceAccount:${google_service_account.api.email}"
}
resource "google_secret_manager_secret_iam_member" "api" {
  for_each  = toset(values(local.api_secrets))
  secret_id = each.value
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.api.email}"
}
resource "google_secret_manager_secret_iam_member" "web" {
  for_each  = toset(values(var.web_secrets))
  secret_id = each.value
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.web.email}"
}
resource "google_cloud_run_v2_service" "api" {
  name                = "algebra-api"
  location            = var.region
  ingress             = "INGRESS_TRAFFIC_INTERNAL_ONLY"
  deletion_protection = true
  template {
    service_account = google_service_account.api.email
    timeout         = "120s"
    scaling {
      min_instance_count = 1
      max_instance_count = 3
    }
    # Reconciliation/refunds run between requests: CPU must remain allocated.
    containers {
      image = var.api_image
      ports {
        container_port = 8080
      }
      resources {
        limits   = { cpu = "1", memory = "1Gi" }
        cpu_idle = false
      }
      dynamic "env" {
        for_each = {
          APP_ENV                        = "production", HTTP_ADDR = ":8080", PUBLIC_WEB_URL = "https://${var.domain}", CORS_ALLOWED_ORIGINS = "https://${var.domain}",
          ENABLED_MERCHANTS              = "none", ECONOMIC_SANDBOX = "off", REDIS_ADDR = "${google_redis_instance.redis.host}:6379",
          EMAIL_FROM                     = var.email_from, PRIVY_APP_ID = var.privy_app_id, TRUSTED_PROXY_HOPS = "2", SOLANA_ALLOW_MAINNET = "yes",
          SOLANA_MAX_PAYMENT_USDC        = "0.05", SPEND_PASS_REQUIRED = "mainnet", SPEND_PASS_RECIPIENTS = "mainnet", SPEND_PASS_PROGRAM_ID = var.program_id,
          SOLANA_MAINNET_KMS_KEY_VERSION = "${google_kms_crypto_key.payer.id}/cryptoKeyVersions/1", ALGEBRA_MASTER_KEY_KMS_KEY = google_kms_crypto_key.master.id
        }
        content {
          name  = env.key
          value = env.value
        }
      }
      dynamic "env" {
        for_each = local.api_secrets
        content {
          name = env.key
          value_source {
            secret_key_ref {
              secret  = env.value
              version = "latest"
            }
          }
        }
      }
      startup_probe {
        http_get {
          path = "/readyz"
        }
        period_seconds    = 10
        failure_threshold = 24
      }
      liveness_probe {
        http_get {
          path = "/healthz"
        }
      }
    }
    vpc_access {
      egress = "ALL_TRAFFIC"
      network_interfaces {
        network    = google_compute_network.private.name
        subnetwork = google_compute_subnetwork.services.name
      }
    }
  }
  depends_on = [google_kms_crypto_key_iam_member.sign, google_kms_crypto_key_iam_member.decrypt, google_secret_manager_secret_iam_member.api, google_compute_router_nat.egress]
}
resource "google_cloud_run_v2_service" "web" {
  name                = "algebra-web"
  location            = var.region
  ingress             = "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER"
  deletion_protection = true
  template {
    service_account = google_service_account.web.email
    timeout         = "120s"
    containers {
      image = var.web_image
      ports {
        container_port = 3000
      }
      env {
        name  = "ALGEBRA_API_URL"
        value = var.api_url
      }
      dynamic "env" {
        for_each = var.web_secrets
        content {
          name = env.key
          value_source {
            secret_key_ref {
              secret  = env.value
              version = "latest"
            }
          }
        }
      }
    }
    vpc_access {
      egress = "ALL_TRAFFIC"
      network_interfaces {
        network    = google_compute_network.private.name
        subnetwork = google_compute_subnetwork.services.name
      }
    }
  }
  depends_on = [google_cloud_run_v2_service.api, google_secret_manager_secret_iam_member.web]
}
# Ingress gates access; the private API's session/agent auth still applies.
resource "google_cloud_run_v2_service_iam_member" "api_invoker" {
  name     = google_cloud_run_v2_service.api.name
  location = var.region
  role     = "roles/run.invoker"
  member   = "allUsers"
}
resource "google_cloud_run_v2_service_iam_member" "web_invoker" {
  name     = google_cloud_run_v2_service.web.name
  location = var.region
  role     = "roles/run.invoker"
  member   = "allUsers"
}
resource "google_compute_region_network_endpoint_group" "web" {
  name                  = "algebra-web"
  network_endpoint_type = "SERVERLESS"
  region                = var.region
  cloud_run {
    service = google_cloud_run_v2_service.web.name
  }
}
resource "google_compute_backend_service" "web" {
  name     = "algebra-web"
  protocol = "HTTP"
  backend {
    group = google_compute_region_network_endpoint_group.web.id
  }
}
resource "google_compute_url_map" "web" {
  name            = "algebra-web"
  default_service = google_compute_backend_service.web.id
}
resource "google_compute_managed_ssl_certificate" "web" {
  name = "algebra-web"
  managed {
    domains = [var.domain]
  }
}
resource "google_compute_target_https_proxy" "web" {
  name             = "algebra-web"
  url_map          = google_compute_url_map.web.id
  ssl_certificates = [google_compute_managed_ssl_certificate.web.id]
}
resource "google_compute_global_address" "web" {
  name = "algebra-web"
}
resource "google_compute_global_forwarding_rule" "web" {
  name       = "algebra-web-https"
  ip_address = google_compute_global_address.web.address
  target     = google_compute_target_https_proxy.web.id
  port_range = "443"
}
resource "google_monitoring_notification_channel" "email" {
  display_name = "Algebra operations"
  type         = "email"
  labels       = { email_address = var.alert_email }
}
resource "google_monitoring_uptime_check_config" "web" {
  display_name = "Algebra readiness"
  timeout      = "10s"
  period       = "60s"
  http_check {
    path         = "/readyz"
    port         = 443
    use_ssl      = true
    validate_ssl = true
  }
  monitored_resource {
    type   = "uptime_url"
    labels = { project_id = var.project_id, host = var.domain }
  }
}
resource "google_monitoring_alert_policy" "readiness" {
  display_name          = "Algebra readiness failing"
  combiner              = "OR"
  notification_channels = [google_monitoring_notification_channel.email.id]
  conditions {
    display_name = "HTTPS readiness fails for 5 minutes"
    condition_threshold {
      filter          = "metric.type=\"monitoring.googleapis.com/uptime_check/check_passed\" AND resource.type=\"uptime_url\" AND metric.label.check_id=\"${google_monitoring_uptime_check_config.web.uptime_check_id}\""
      comparison      = "COMPARISON_LT"
      threshold_value = 1
      duration        = "300s"
      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_FRACTION_TRUE"
      }
    }
  }
}
resource "google_monitoring_alert_policy" "errors" {
  display_name          = "Algebra API server errors"
  combiner              = "OR"
  notification_channels = [google_monitoring_notification_channel.email.id]
  conditions {
    display_name = "API 5xx requests"
    condition_threshold {
      filter          = "metric.type=\"run.googleapis.com/request_count\" AND resource.type=\"cloud_run_revision\" AND resource.label.service_name=\"algebra-api\" AND metric.label.response_code_class=\"5xx\""
      comparison      = "COMPARISON_GT"
      threshold_value = 0
      duration        = "300s"
      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_RATE"
      }
    }
  }
}
output "backend_uri" {
  value = google_cloud_run_v2_service.api.uri
}
output "database_private_ip" {
  value = google_sql_database_instance.postgres.private_ip_address
}
output "https_ip" {
  value = google_compute_global_address.web.address
}
output "payer_key_version" {
  value = "${google_kms_crypto_key.payer.id}/cryptoKeyVersions/1"
}
output "master_key" {
  value = google_kms_crypto_key.master.id
}
