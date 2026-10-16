locals {
  services = [
    "container.googleapis.com",
    "artifactregistry.googleapis.com",
    "secretmanager.googleapis.com",
    "iam.googleapis.com",
    "iamcredentials.googleapis.com",
    "sts.googleapis.com",
  ]
}

resource "google_project_service" "apis" {
  for_each           = toset(local.services)
  service            = each.value
  disable_on_destroy = false
}

resource "google_container_cluster" "agentgopher" {
  name     = var.cluster_name
  location = var.region

  enable_autopilot    = true
  deletion_protection = false

  release_channel {
    channel = "REGULAR"
  }

  workload_identity_config {
    workload_pool = "${var.project_id}.svc.id.goog"
  }

  secret_manager_config {
    enabled = true
  }

  depends_on = [google_project_service.apis]
}

resource "google_artifact_registry_repository" "images" {
  location      = var.region
  repository_id = "agentgopher"
  format        = "DOCKER"
  description   = "agentgopher runner images"

  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret" "runner" {
  for_each  = toset(var.secret_names)
  secret_id = each.value

  replication {
    auto {}
  }

  depends_on = [google_project_service.apis]
}
