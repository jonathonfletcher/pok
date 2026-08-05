# IAM for the border's LB failover agent (docs/README.md §5.2, tf/aws/border/lb-failover.sh).
#
# The agent must call ec2:ReplaceRoute to repoint the Cilium-LB route at a live node.
# This is the ONE new AWS resource the active/standby design needs — and IAM roles /
# policies / instance profiles are FREE (unlike an NLB or ECR). The policy is scoped as
# tightly as EC2 allows: ReplaceRoute only on THIS route table; the Describe* calls the
# agent uses to map node IP <-> ENI don't support resource-level scoping, so they are
# read-only on "*". No long-lived keys — creds are delivered to the instance via IMDS.

data "aws_iam_policy_document" "border_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

data "aws_iam_policy_document" "border_lb_failover" {
  statement {
    sid       = "ReplaceRouteOnLbRouteTable"
    actions   = ["ec2:ReplaceRoute"]
    resources = [aws_route_table.vpc.arn]
  }
  statement {
    sid       = "DescribeForIpEniMapping"
    actions   = ["ec2:DescribeRouteTables", "ec2:DescribeNetworkInterfaces"]
    resources = ["*"] # EC2 Describe* has no resource-level scoping
  }
}

resource "aws_iam_role" "border_lb_failover" {
  name               = "${var.environment}-border-lb-failover"
  assume_role_policy = data.aws_iam_policy_document.border_assume.json
}

resource "aws_iam_role_policy" "border_lb_failover" {
  name   = "lb-failover"
  role   = aws_iam_role.border_lb_failover.id
  policy = data.aws_iam_policy_document.border_lb_failover.json
}

resource "aws_iam_instance_profile" "border" {
  name = "${var.environment}-border"
  role = aws_iam_role.border_lb_failover.name
}
