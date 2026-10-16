# Running agentgopher on GKE

## One-time setup

1. Create the infrastructure: see `deploy/terraform/README.md`. Note the outputs.
2. Add secret values (never in the repo):
   ```bash
   claude setup-token | gcloud secrets versions add agentgopher-claude-code-oauth-token --data-file=-
   printf '%s' "$GH_TOKEN" | gcloud secrets versions add agentgopher-gh-token --data-file=-
   ```
   The GitHub token needs `contents: write` and `pull_requests: write` on the repos the agent works on.
3. Point kubectl at the cluster: run the `get_credentials_command` output. This needs `gke-gcloud-auth-plugin` (`gcloud components install gke-gcloud-auth-plugin`).
4. Apply the manifests, filling in the two placeholders:
   ```bash
   RUNNER_SA=$(terraform -chdir=deploy/terraform output -raw runner_service_account_email)
   PROJECT_ID=$(gcloud config get-value project)
   kubectl kustomize deploy/k8s \
     | sed "s/REPLACE_WITH_RUNNER_SERVICE_ACCOUNT_EMAIL/$RUNNER_SA/; s/PROJECT_ID/$PROJECT_ID/g" \
     | kubectl apply -f -
   ```
5. Set the GitHub repository secrets for the release workflow:
   ```bash
   gh secret set GCP_WORKLOAD_IDENTITY_PROVIDER --body "$(terraform -chdir=deploy/terraform output -raw github_workload_identity_provider)"
   gh secret set GCP_PUBLISHER_SERVICE_ACCOUNT --body "$(terraform -chdir=deploy/terraform output -raw github_publisher_service_account)"
   gh secret set GCP_PROJECT_ID --body "$PROJECT_ID"
   gh secret set GCP_REGION --body "<region>"
   ```
6. Publish an image: `git tag v0.1.0 && git push origin v0.1.0`. The workflow pushes `<image_repository>/runner:v0.1.0` and `:latest`.

## Starting a run

```bash
export AGENTGOPHER_IMAGE=$(terraform -chdir=deploy/terraform output -raw image_repository)/runner:latest
agentgopher remote run --repo https://github.com/OWNER/REPO.git --script .agentgopher
```

The command creates a Job, follows its log and ends with the pull request
links. Ctrl-C leaves the Job running and prints the `kubectl logs` command to
follow it. Jobs are deleted 24 hours after they finish.

The target repo needs an `.agentgopher/` directory from `agentgopher init`.
The entrypoint runs the script from that directory (`go run .`), seeds
`.env` from `.env.example` when it is missing, then pushes `AGENTGOPHER_BRANCH`
and opens the PR. A script using the merge-to-head strategy leaves its commits
on the base branch; the entrypoint moves them onto the run branch. A script
that wants its own branch reads `AGENTGOPHER_BRANCH` and uses the named
strategy with it.

## Sandboxes inside the cluster

The Job has no Docker daemon, so the `docker.New(nil)` default of the init
templates does not work there. A script that runs in the cluster must use the
Kubernetes provider; the runner image doubles as the sandbox image and is
passed in as `AGENTGOPHER_IMAGE`:

```go
var sb sandbox.Provider = docker.New(nil)
if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
	sb = kubernetes.New(&kubernetes.Options{Image: os.Getenv("AGENTGOPHER_IMAGE")})
}
```
The Job's service account may create Pods in the
`agentgopher` namespace (see `deploy/k8s/rbac.yaml`). Leftover sandbox Pods
are labelled `app.kubernetes.io/component=sandbox` and expire after
`Options.PodTTL` (default 2h):

```bash
kubectl delete pods -n agentgopher -l app.kubernetes.io/component=sandbox
```
