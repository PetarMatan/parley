output "public_ip" {
  description = "Elastic IP of the host"
  value       = aws_eip.host.public_ip
}

output "instance_id" {
  description = "EC2 instance id, used by stop.sh and start.sh"
  value       = aws_instance.host.id
}

output "ssh_command" {
  description = "SSH as the ubuntu user"
  value       = "ssh ubuntu@${aws_eip.host.public_ip}"
}

output "urls" {
  description = "Public names of the services"
  value       = { for h in local.hosts : h => "https://${h}.${var.domain}" }
}

output "region" {
  description = "Region of the host, used by stop.sh and start.sh"
  value       = var.region
}
