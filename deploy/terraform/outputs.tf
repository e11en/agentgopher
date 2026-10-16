output "cluster_name" {
  value = google_container_cluster.agentgopher.name
}

output "get_credentials_command" {
  value = "gcloud container clusters get-credentials ${google_container_cluster.agentgopher.name} --region ${var.region} --project ${var.project_id}"
}

output "runner_service_account_email" {
  value = google_service_account.runner.email
}

output "image_repository" {
  value = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.images.repository_id}"
}

output "github_workload_identity_provider" {
  value = google_iam_workload_identity_pool_provider.github.name
}

output "github_publisher_service_account" {
  value = google_service_account.publisher.email
}
