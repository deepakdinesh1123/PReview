terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

variable "github_repository" {
  description = "(Optional) GitHub repository (owner/name) allowed to assume the deployer role. Leave empty if using Access Keys."
  type        = string
  default     = ""
}

variable "github_oidc_provider_arn" {
  description = "ARN of the GitHub OIDC provider. Leave empty to create one."
  type        = string
  default     = ""
}

# 1. S3 Bucket for Build Artifacts
resource "aws_s3_bucket" "artifacts" {
  bucket_prefix = "preview-artifacts-"
}

# 2. Build Role (Assumed by Lambda MicroVMs to build the image)
resource "aws_iam_role" "builder" {
  name = "PReviewBuildRole"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = [
            "lambda.amazonaws.com"
          ]
        }
      }
    ]
  })
}

resource "aws_iam_role_policy" "builder_policy" {
  name = "PReviewBuildPolicy"
  role = aws_iam_role.builder.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "s3:GetObject"
        ]
        Resource = "${aws_s3_bucket.artifacts.arn}/*"
      },
      {
        Effect = "Allow"
        Action = [
          "logs:CreateLogStream",
          "logs:PutLogEvents",
          "logs:CreateLogGroup"
        ]
        Resource = "arn:aws:logs:*:*:*"
      }
    ]
  })
}

# 3. Managed Policy for Deployer
resource "aws_iam_policy" "deployer_policy" {
  name        = "PReviewDeployerPolicy"
  description = "Policy containing all permissions required to deploy MicroVM previews"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "lambdamicrovms:CreateMicrovmImage",
          "lambdamicrovms:UpdateMicrovmImage",
          "lambdamicrovms:GetMicrovmImage",
          "lambdamicrovms:ListMicrovmImages",
          "lambdamicrovms:DeleteMicrovmImage",
          "lambdamicrovms:RunMicrovm",
          "lambdamicrovms:GetMicrovm",
          "lambdamicrovms:ListMicrovms",
          "lambdamicrovms:TerminateMicrovm",
          "lambdamicrovms:CreateMicrovmAuthToken",
          "lambdamicrovms:TagResource",
          "lambdamicrovms:WaitMicrovmRunning"
        ]
        Resource = "*"
      },
      {
        Effect = "Allow"
        Action = [
          "s3:PutObject",
          "s3:DeleteObject"
        ]
        Resource = "${aws_s3_bucket.artifacts.arn}/*"
      },
      {
        Effect = "Allow"
        Action = [
          "s3:ListBucket"
        ]
        Resource = aws_s3_bucket.artifacts.arn
      },
      {
        Effect = "Allow"
        Action = "iam:PassRole"
        Resource = aws_iam_role.builder.arn
      }
    ]
  })
}


# 4. GitHub OIDC Provider (only if requested and repo is provided)
resource "aws_iam_openid_connect_provider" "github" {
  count = var.github_repository != "" && var.github_oidc_provider_arn == "" ? 1 : 0

  url             = "https://token.actions.githubusercontent.com"
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = ["6938fd4d98bab03faadb97b34396831e3780aea1", "1c58a3a8518e8759bf075b76b750d4f2df264fcd"]
}

locals {
  github_oidc_arn = var.github_repository != "" ? (var.github_oidc_provider_arn == "" ? aws_iam_openid_connect_provider.github[0].arn : var.github_oidc_provider_arn) : ""
}

# 5. Deployer Role (Assumed by GitHub Actions workflow via OIDC)
resource "aws_iam_role" "deployer" {
  count = var.github_repository != "" ? 1 : 0
  name  = "PReviewDeployerRole"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = {
          Federated = local.github_oidc_arn
        }
        Action = "sts:AssumeRoleWithWebIdentity"
        Condition = {
          StringLike = {
            "token.actions.githubusercontent.com:sub" : "repo:${var.github_repository}:*"
          },
          StringEquals = {
            "token.actions.githubusercontent.com:aud" : "sts.amazonaws.com"
          }
        }
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "deployer_attach" {
  count      = var.github_repository != "" ? 1 : 0
  role       = aws_iam_role.deployer[0].name
  policy_arn = aws_iam_policy.deployer_policy.arn
}

output "deployer_managed_policy_arn" {
  description = "If using Access Keys, attach this Managed Policy to your IAM User"
  value       = aws_iam_policy.deployer_policy.arn
}

output "deployer_role_arn" {
  description = "If using OIDC, add this to your GitHub workflow (role-to-assume)"
  value       = var.github_repository != "" ? aws_iam_role.deployer[0].arn : ""
}

output "build_role_arn" {
  description = "Add this to your preview.yml workflow (buildRoleARN)"
  value       = aws_iam_role.builder.arn
}

output "s3_path" {
  description = "Add this to your preview.yml workflow (s3Path)"
  value       = "s3://${aws_s3_bucket.artifacts.id}/previews"
}

# 6. IAM User for GitHub Actions (since OIDC is not used)
resource "aws_iam_user" "github_deployer" {
  count = var.github_repository == "" ? 1 : 0
  name  = "PReviewGitHubActionsUser"
}

resource "aws_iam_user_policy_attachment" "github_deployer_attach" {
  count      = var.github_repository == "" ? 1 : 0
  user       = aws_iam_user.github_deployer[0].name
  policy_arn = aws_iam_policy.deployer_policy.arn
}

resource "aws_iam_access_key" "github_deployer_key" {
  count = var.github_repository == "" ? 1 : 0
  user  = aws_iam_user.github_deployer[0].name
}

output "aws_access_key_id" {
  description = "AWS Access Key ID for GitHub Actions secrets (only if using Access Keys)"
  value       = var.github_repository == "" ? aws_iam_access_key.github_deployer_key[0].id : ""
}

output "aws_secret_access_key" {
  description = "AWS Secret Access Key for GitHub Actions secrets (only if using Access Keys)"
  value       = var.github_repository == "" ? aws_iam_access_key.github_deployer_key[0].secret : ""
  sensitive   = true
}
