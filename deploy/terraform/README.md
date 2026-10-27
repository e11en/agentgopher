# agentgopher on GKE

Creates a new GKE Autopilot cluster with Workload Identity and the Secret
Manager add-on, an Artifact Registry repository, the runner service account,
the two Secret Manager secrets (empty; add versions yourself) and Workload
Identity Federation for the release workflow. The cluster has the managed
Agent Sandbox add-on, which installs the Sandbox CRDs and controller; gVisor
is on by default on Autopilot.

```bash
cp terraform.tfvars.example terraform.tfvars   # fill in project_id and region
terraform init
terraform plan
terraform apply
```

State is local by default. Add a GCS backend block to `versions.tf` before
sharing this setup with others.

See `docs/kubernetes.md` for the steps after apply.
