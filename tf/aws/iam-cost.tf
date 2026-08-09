# IAM for the awscost poller (apps/awscost). It reads billing/inventory data account-wide and
# needs NO write access. Creds are delivered to ONE worker via an instance profile (IMDS, no
# long-lived keys) — the same pattern as the border's lb-failover role, and IAM roles/policies/
# instance-profiles are free.
#
# Least-privilege: exactly the four read-only actions the poller calls. Cost Explorer, Pricing
# and EC2 Describe* are account/region-global reads with no resource-level scoping, so the
# resources must be "*" (same rationale as iam-border.tf's Describe* statement).

data "aws_iam_policy_document" "cost_reader" {
  statement {
    sid = "CostAndPricingRead"
    actions = [
      "ce:GetCostAndUsage",  # ACTUAL: daily UnblendedCost
      "pricing:GetProducts", # ESTIMATED: on-demand instance/EBS rates
    ]
    resources = ["*"]
  }
  statement {
    sid = "Ec2DescribeForInventory"
    actions = [
      "ec2:DescribeInstances", # ESTIMATED: every instance (k8s or not), type/state/launch/public-IP
      "ec2:DescribeVolumes",   # ESTIMATED: attached EBS type + size
    ]
    resources = ["*"] # EC2 Describe* has no resource-level scoping
  }
}

# Reuse the ec2.amazonaws.com AssumeRole trust document from iam-border.tf — an EC2 instance
# profile assumes the role, so the same trust policy applies.
resource "aws_iam_role" "cost_reader" {
  name               = "${var.environment}-cost-reader"
  assume_role_policy = data.aws_iam_policy_document.border_assume.json
}

resource "aws_iam_role_policy" "cost_reader" {
  name   = "cost-reader"
  role   = aws_iam_role.cost_reader.id
  policy = data.aws_iam_policy_document.cost_reader.json
}

resource "aws_iam_instance_profile" "cost_reader" {
  name = "${var.environment}-cost-reader"
  role = aws_iam_role.cost_reader.name
}
