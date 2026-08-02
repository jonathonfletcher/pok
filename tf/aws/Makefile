# OpenTofu driver for the AWS infrastructure root.
#
# Credentials come from the environment (AWS_PROFILE / AWS_* / SSO) — none are
# stored in the repo. `plan`/`apply` fail without them (the aws_ami data source
# makes a live DescribeImages call). Values come from terraform.tfvars.json.
#
# apply is left interactive on purpose: this provisions real, billable cloud
# infrastructure — review the plan and confirm. (Do not restructure this directory
# or its state while an apply is running.)

.PHONY: init plan apply teardown destroy destroy-keep-eip fmt validate ssh-open ssh-close

# Auto-init guard. Any target that talks to the providers gets an order-only prereq on
# `.terraform` (`target: | .terraform`); if that provider cache is absent, this rule runs
# `tofu init` first — so a fresh checkout, or the sanitized public mirror, doesn't fail with
# "Required plugins are not installed". Keyed on `.terraform/` ONLY: the absence of a
# `*.tfstate` means "nothing applied yet" (also true right after init, and for remote-state
# roots), NOT "uninitialized" — keying on state would re-init spuriously. (If `.terraform`
# exists but is stale vs the lock, tofu still prints its own "run tofu init"; run `make init`.)
.terraform:
	tofu init

init:
	tofu init

plan: | .terraform
	tofu plan

apply: | .terraform
	tofu apply

destroy: | .terraform
	tofu destroy

# The provider-agnostic teardown entrypoint (called by the top-level Makefile). Keeps
# the border EIP so pok.somegroup.net survives the rebuild.
teardown: destroy-keep-eip

# Tear everything down EXCEPT the border EIP, so its public IP (pok.somegroup.net)
# survives — a plain `destroy` errors on the protected EIP by design. Re-`apply`
# afterwards re-associates the same allocation to the new border instance.
destroy-keep-eip: | .terraform
	tofu destroy -exclude='module.border.aws_eip.vm[0]'

fmt:
	tofu fmt -recursive

validate: | .terraform
	tofu validate

# Toggle border SSH (22/tcp from 0.0.0.0/0), scoped to the SSH ingress rule (-target) so it is
# fast and touches nothing else. The choice is PERSISTED to border-ssh.auto.tfvars.json — which
# tofu auto-loads on EVERY run — so a later plain `make infra`/`apply` keeps it: "closed" is
# durable, not silently reopened. Open for setup/management; close during normal operation.
# (gitignored: matched by *.tfvars.json)
ssh-open: | .terraform
	printf '{ "border_allow_ssh": true }\n' > border-ssh.auto.tfvars.json
	tofu apply -target='aws_vpc_security_group_ingress_rule.border_ssh' -auto-approve
	@echo ">>> border SSH OPEN (persisted in border-ssh.auto.tfvars.json)"

ssh-close: | .terraform
	printf '{ "border_allow_ssh": false }\n' > border-ssh.auto.tfvars.json
	tofu apply -target='aws_vpc_security_group_ingress_rule.border_ssh' -auto-approve
	@echo ">>> border SSH CLOSED (persisted; survives make infra)"
