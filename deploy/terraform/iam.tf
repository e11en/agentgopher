# Google service account the Job's Kubernetes service account impersonates.
resource "google_service_account" "runner" {
  account_id   = "agentgopher-runner"
  display_name = "agentgopher runner"
}

resource "google_service_account_iam_member" "runner_workload_identity" {
  service_account_id = google_service_account.runner.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "serviceAccount:${var.project_id}.svc.id.goog[agentgopher/agentgopher-runner]"
}

resource "google_secret_manager_secret_iam_member" "runner_reads_secrets" {
  for_each  = google_secret_manager_secret.runner
  secret_id = each.value.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.runner.email}"
}

# Autopilot nodes pull images with the project's default compute service account.
data "google_project" "current" {}

resource "google_artifact_registry_repository_iam_member" "nodes_pull" {
  location   = google_artifact_registry_repository.images.location
  repository = google_artifact_registry_repository.images.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${data.google_project.current.number}-compute@developer.gserviceaccount.com"
}
