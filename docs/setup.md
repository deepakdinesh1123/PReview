# Setup & usage

## 1. AWS prerequisites

PReview requires an S3 bucket for artifacts and two IAM roles (one for the GitHub Action to assume, and one for the MicroVM build process to assume).

We provide a CloudFormation template to quickly create all of these resources with the exact least-privilege permissions required.

1. **[Click here to deploy the setup in your AWS account](https://console.aws.amazon.com/cloudformation/home?region=us-east-2#/stacks/quickcreate?templateURL=https://raw.githubusercontent.com/deepakdinesh1123/PReview/main/cloudformation.yml&stackName=PReview-Setup)**
2. **If using OIDC (Recommended):** Enter your `GitHubRepository` (e.g., `octocat/Hello-World`).
   **If using existing AWS Access Keys:** Leave `GitHubRepository` blank.
3. Scroll to the bottom, check the capability acknowledgment box, and click **Create stack**.
4. Once the stack status changes to `CREATE_COMPLETE`, click on the **Outputs** tab.
5. If using OIDC, you will get: `DeployerRoleARN`, `BuildRoleARN`, and `S3Path`.
6. If using Access Keys, you will get `DeployerManagedPolicyARN` (attach this policy to your IAM User in AWS), `BuildRoleARN`, and `S3Path`.

*(If you prefer not to use CloudFormation, you can inspect `cloudformation.yml` or run the OpenTofu/Terraform script located in the `terraform/` directory).*

Note that the AWS Region must support Lambda MicroVMs (the default base image ARN is in `us-east-2`). Set `AWS_REGION` accordingly in your Action.

## 2. Make your app deployable

Put a `Dockerfile` at the root of the directory you deploy (`appPath`, default `.`). The app must listen on the configured `port` (default `8080`) over HTTP:

```dockerfile
FROM node:24-alpine
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
EXPOSE 8080
CMD ["node", "server.js"]
```

The build context is the zipped directory, minus `.git`, `.github` and `node_modules`.

## 3. Add the workflow

Copy [`examples/preview.yml`](../examples/preview.yml) to `.github/workflows/` and create the label (default `preview`) in your repository.

| Action | Result |
|---|---|
| Add the `preview` label | A "deploying" comment appears, then the live link. |
| Push to the PR | The existing preview is rebuilt as a new image version and swapped in. |
| Remove the label / close the PR | VM terminated, image and artifacts deleted, comment updated. |

### Action inputs

| Input | Default | Description |
|---|---|---|
| `buildRoleARN` | (required) | Role Lambda assumes to build the image. |
| `s3Path` | (required) | `s3://bucket/prefix` for artifacts. |
| `baseImageARN` | `...al2023-1` (us-east-2) | Lambda-managed base image. |
| `executionRoleARN` | | Role the running MicroVM assumes. |
| `label` | `preview` | Label that enables previews. |
| `githubToken` | `github.token` | Needs `pull-requests: write`. |
| `appPath` | `.` | Directory containing the Dockerfile. |
| `port` | `8080` | In-VM application port. |
| `memory` | `2048` | MiB. |
| `ttl` | `8h` | Max lifetime (platform cap 8h; `infinite` = cap). |
| `idleDuration` | `15m` | Suspend after inactivity. |
| `suspendedDuration` | `1h` | Terminate after this long suspended. |
| `namePrefix` | `preview` | Image-name prefix / safety scope. |
| `ingressNetworkConnectors` / `egressNetworkConnectors` | | Comma-separated connector names. |
| `debug` | `false` | Verbose logs. |

Outputs: `live`, `url`, `name`, `microvm_id`.

## 4. Troubleshooting

| Symptom | Likely cause |
|---|---|
| `no Dockerfile found at the root of ...` | `appPath` points at the wrong directory. |
| Image build fails | Check the build-role permissions and the Dockerfile; the failure reason is shown in the PR comment. |
| Comment never appears | Missing `pull-requests: write` permission (a warning is logged; deploy continues). |
| Link returns 404 | No live MicroVM for that name (TTL expired or torn down). Re-add the label / push a commit. |
| Preview ignored | Fork PR, or the PR lacks the label. See the `decision=` log line. |
