variable "region" {
  description = "AWS region. The account's service control policy allows EC2 only in eu-north-1 (Stockholm)."
  type        = string
  default     = "eu-north-1"
}

variable "profile" {
  description = "AWS CLI profile. Always the dedicated testbed profile, never a work profile."
  type        = string
  default     = "parley"
}

variable "domain" {
  description = "Registered domain whose hosted zone is in Route 53."
  type        = string
  default     = "parleylab.dev"
}

variable "zone_id" {
  description = "Route 53 hosted zone id of the domain."
  type        = string
  default     = "Z08406361U13I2IELVQUT"
}

variable "instance_type" {
  description = "EC2 instance type. GitLab CE alone needs 4 GB; 16 GB gives headroom for Keycloak, the runner and builds."
  type        = string
  default     = "t3.xlarge"
}

variable "root_volume_gb" {
  description = "Size of the root volume in GB. GitLab data, images and build caches live on it."
  type        = number
  default     = 80
}

variable "admin_cidr" {
  description = "CIDR allowed to reach SSH (port 22). Use your public address as /32: curl -s https://checkip.amazonaws.com"
  type        = string

  validation {
    condition     = can(cidrhost(var.admin_cidr, 0))
    error_message = "admin_cidr must be a CIDR such as 203.0.113.1/32."
  }
}

variable "admin_public_key_path" {
  description = "Path of the SSH public key installed for the ubuntu user."
  type        = string
  default     = "~/.ssh/id_ed25519.pub"
}

variable "repo_url" {
  description = "Git URL of the repository that holds deploy/cloud. Cloned onto the host at boot."
  type        = string
  default     = "https://github.com/PetarMatan/parley.git"
}

variable "repo_ref" {
  description = "Branch, tag or commit of repo_url to check out on the host."
  type        = string
  default     = "main"
}

variable "project_tag" {
  description = "Value of the Project tag put on every resource."
  type        = string
  default     = "parley-testbed"
}
