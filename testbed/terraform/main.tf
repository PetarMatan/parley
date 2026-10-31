# One host runs the whole shared side of the testbed under Docker Compose:
# reverse proxy, Keycloak, GitLab CE, agentgateway, inbox. See parley-testbed.md.

data "aws_ssm_parameter" "ubuntu_ami" {
  name = "/aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp3/ami-id"
}

data "aws_vpc" "default" {
  default = true
}

data "aws_subnets" "default" {
  filter {
    name   = "vpc-id"
    values = [data.aws_vpc.default.id]
  }

  filter {
    name   = "default-for-az"
    values = ["true"]
  }
}

locals {
  name   = var.project_tag
  subnet = sort(data.aws_subnets.default.ids)[0]
  hosts  = ["auth", "gitlab", "channel"]
}

resource "aws_key_pair" "admin" {
  key_name   = "${local.name}-admin"
  public_key = file(pathexpand(var.admin_public_key_path))
}

resource "aws_security_group" "host" {
  name        = "${local.name}-host"
  description = "Testbed host: web from anywhere, SSH from the admin address"
  vpc_id      = data.aws_vpc.default.id

  tags = {
    Name = "${local.name}-host"
  }
}

resource "aws_vpc_security_group_ingress_rule" "http_v4" {
  security_group_id = aws_security_group.host.id
  description       = "HTTP, needed for the ACME HTTP-01 challenge and redirects"
  ip_protocol       = "tcp"
  from_port         = 80
  to_port           = 80
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_ingress_rule" "http_v6" {
  security_group_id = aws_security_group.host.id
  description       = "HTTP over IPv6"
  ip_protocol       = "tcp"
  from_port         = 80
  to_port           = 80
  cidr_ipv6         = "::/0"
}

resource "aws_vpc_security_group_ingress_rule" "https_v4" {
  security_group_id = aws_security_group.host.id
  description       = "HTTPS"
  ip_protocol       = "tcp"
  from_port         = 443
  to_port           = 443
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_ingress_rule" "https_v6" {
  security_group_id = aws_security_group.host.id
  description       = "HTTPS over IPv6"
  ip_protocol       = "tcp"
  from_port         = 443
  to_port           = 443
  cidr_ipv6         = "::/0"
}

resource "aws_vpc_security_group_ingress_rule" "ssh_admin" {
  security_group_id = aws_security_group.host.id
  description       = "SSH from the admin address only"
  ip_protocol       = "tcp"
  from_port         = 22
  to_port           = 22
  cidr_ipv4         = var.admin_cidr
}

resource "aws_vpc_security_group_egress_rule" "all_v4" {
  security_group_id = aws_security_group.host.id
  description       = "All outbound"
  ip_protocol       = "-1"
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_egress_rule" "all_v6" {
  security_group_id = aws_security_group.host.id
  description       = "All outbound over IPv6"
  ip_protocol       = "-1"
  cidr_ipv6         = "::/0"
}

resource "aws_instance" "host" {
  ami                         = data.aws_ssm_parameter.ubuntu_ami.value
  instance_type               = var.instance_type
  subnet_id                   = local.subnet
  vpc_security_group_ids      = [aws_security_group.host.id]
  key_name                    = aws_key_pair.admin.key_name
  associate_public_ip_address = true

  # Nothing secret goes through user data. Secrets arrive later over SSH (scripts/deploy.sh).
  user_data = templatefile("${path.module}/cloud-init.yaml", {
    repo_url = var.repo_url
    repo_ref = var.repo_ref
  })
  user_data_replace_on_change = false

  metadata_options {
    http_tokens                 = "required"
    http_endpoint               = "enabled"
    http_put_response_hop_limit = 1
  }

  root_block_device {
    volume_type           = "gp3"
    volume_size           = var.root_volume_gb
    encrypted             = true
    delete_on_termination = true
  }

  tags = {
    Name = "${local.name}-host"
  }

  lifecycle {
    # A new AMI in the SSM parameter must not replace a host that holds GitLab data.
    ignore_changes = [ami]
  }
}

resource "aws_eip" "host" {
  domain = "vpc"

  tags = {
    Name = "${local.name}-host"
  }
}

resource "aws_eip_association" "host" {
  instance_id   = aws_instance.host.id
  allocation_id = aws_eip.host.id
}
