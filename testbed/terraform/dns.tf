# One A record per public name, all to the Elastic IP. The address survives
# stop and start of the instance, so the records never change.

resource "aws_route53_record" "hosts" {
  for_each = toset(local.hosts)

  zone_id = var.zone_id
  name    = "${each.key}.${var.domain}"
  type    = "A"
  ttl     = 60
  records = [aws_eip.host.public_ip]
}
