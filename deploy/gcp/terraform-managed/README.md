# Managed-services Terraform (earlier draft, not what production runs)

Production runs on one hardened VM: see [../README.md](../README.md).

This module is the earlier plan from 2026-10-10: Cloud SQL, Memorystore Redis,
Cloud KMS keys, Secret Manager, a global address, uptime checks and alerts. It
validated with `terraform validate` then and was never applied. Kept for when
the VM outgrows itself; review it against the current deployment before use.

    terraform init -backend-config="bucket=<private state bucket>"
    terraform plan -var-file=production.tfvars
