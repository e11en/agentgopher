variable "project_id" {
  description = "GCP project that hosts the cluster. No default on purpose."
  type        = string
}

variable "region" {
  description = "Region of the Autopilot cluster and the Artifact Registry repository, for example europe-west4."
  type        = string
}

variable "cluster_name" {
  description = "Name of the GKE Autopilot cluster."
  type        = string
  default     = "agentgopher"
}

variable "github_repository" {
  description = "GitHub repository allowed to push images through Workload Identity Federation, as owner/name."
  type        = string
  default     = "e11en/agentgopher"
}

variable "secret_names" {
  description = "Secret Manager secret ids the runner may read. Values are added outside Terraform."
  type        = list(string)
  default     = ["agentgopher-claude-code-oauth-token", "agentgopher-gh-token"]
}
