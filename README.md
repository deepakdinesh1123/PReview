# PReview

Preview environments for pull requests on **AWS Lambda MicroVMs**.

Add a label to a PR, and PReview builds your repo's `Dockerfile` into a MicroVM image, runs it, and posts a live link in the PR so teammates can try the change. Push again and it redeploys; remove the label or close the PR and everything is cleaned up. Inspired by [pullpreview](https://github.com/pullpreview/action).

## How it works

1. Label a PR with `preview` -> a "deploying" comment appears.
2. PReview zips the app directory, uploads it to S3 and builds a MicroVM image (new version on each push).
3. A MicroVM is started from that version; the previous one is retired only after the new one is running.
4. The comment is updated with the raw endpoint URL and an `X-aws-proxy-auth` token (valid for 60 minutes) to access the environment securely via API clients.
5. Unlabel/close -> VM terminated, image and artifacts deleted.

## Quickstart

### 1. Provision AWS Resources (1-Click)
PReview provides a CloudFormation template to automatically provision the secure IAM roles and S3 bucket you need.

[👉 **Click here to deploy the resources in your AWS account**](https://console.aws.amazon.com/cloudformation/home?region=us-east-2#/stacks/quickcreate?templateURL=https://raw.githubusercontent.com/deepakdinesh1123/PReview/main/cloudformation.yml&stackName=PReview-Setup)

1. **If using OIDC (Recommended)**: Enter your **GitHubRepository** (e.g., `deepakdinesh1123/my-repo`).
   **If using Access Keys**: Leave GitHubRepository blank.
2. Check the acknowledgment box at the bottom and click **Create stack**.
3. Once the stack completes, go to the **Outputs** tab to get your ARNs and S3 Path.
   *(If using Access Keys, attach the `DeployerManagedPolicyARN` to your existing IAM User in the AWS console).*

*(Prefer Terraform? See `terraform/main.tf`)*

### 2. Configure GitHub Actions
Create `.github/workflows/preview.yml` in your repository and configure it based on your auth method:

#### Option A: OIDC (Recommended)
```yaml
- uses: aws-actions/configure-aws-credentials@v4
  with: 
    role-to-assume: <DeployerRoleARN>
    aws-region: us-east-2
- uses: deepakdinesh1123/PReview@main
  with:
    buildRoleARN: <BuildRoleARN>
    s3Path: <S3Path>
```

#### Option B: AWS Access Keys
If your repository already has AWS credentials configured in GitHub Secrets:
```yaml
- uses: aws-actions/configure-aws-credentials@v4
  with:
    aws-access-key-id: ${{ secrets.AWS_ACCESS_KEY_ID }}
    aws-secret-access-key: ${{ secrets.AWS_SECRET_ACCESS_KEY }}
    aws-region: us-east-2
- uses: deepakdinesh1123/PReview@main
  with:
    buildRoleARN: <BuildRoleARN>
    s3Path: <S3Path>
```

Full workflow: [`examples/preview.yml`](examples/preview.yml).

## Docs

- [Setup & usage](docs/setup.md): AWS prerequisites, workflow, inputs, troubleshooting
- [Architecture & design](docs/architecture.md): flows, decisions, limitations
- [AGENTS.md](AGENTS.md): contributor/agent guide

## Status

Early. Logic is unit-tested (`go test ./...`), but the end-to-end path has not been verified against live AWS yet, so expect to adjust on the first real deploy. See [limitations](docs/architecture.md#known-limitations--assumptions).

## License

See [LICENSE](LICENSE).