<!-- dflook/terraform-github-actions {"workspace":"engineering-sbg5","backend_type":"s3","label":"infrastructure/edge / engineering-sbg5","backend":"0000","plan_job_ref":"Job Terraform Plan #1 at https://github.com/example/infra/actions/runs/1","plan_text_format":"diff","version":"3.0.0"} -->
Terraform plan for __infrastructure/edge / engineering-sbg5__
<details>
<summary>Plan: 1 to add, 3 to change, 0 to destroy.</summary>

```diff
Terraform used the selected providers to generate the following execution
plan. Resource actions are indicated with the following symbols:
+   create
!~  update in-place

Terraform will perform the following actions:

  # cloudflare_access_application.monitoring will be updated in-place
!~  resource "cloudflare_access_application" "monitoring" {
        id                           = "00000000-0000-0000-0000-000000000001"
!~      name                         = "[TF] APM Monitoring" -> "[TF] Kibana Monitoring"
!~      policies                     = [
+           "00000000-0000-0000-0000-000000000002",
            "00000000-0000-0000-0000-000000000003",
        ]
        tags                         = []
#        (24 unchanged attributes hidden)

!~      include {
!~          ip                      = [
#                (2 unchanged elements hidden)
+               "192.0.2.10/32",
            ]
        }
    }

  # cloudflare_record.monitoring_cname will be updated in-place
!~  resource "cloudflare_record" "monitoring_cname" {
        id              = "0000000000000000000000000000000a"
        name            = "monitoring"
!~      proxied         = false -> true
!~      modified_on     = "2026-06-11T15:49:52Z" -> (known after apply)
        tags            = []
#        (11 unchanged attributes hidden)
    }

  # module.save_users["alice"].vault_kv_secret_v2.ovh_user will be updated in-place
!~  resource "vault_kv_secret_v2" "ovh_user" {
!~      data_json           = (sensitive value)
!~      api_token           = "abc" -> "def"
        id                  = "safe/data/example/alice"
#        (7 unchanged attributes hidden)
#        (1 unchanged block hidden)
    }

  # module.object_storage["mobile-qa-builds"].aws_s3_bucket.bucket will be created
+   resource "aws_s3_bucket" "bucket" {
+       acceleration_status         = (known after apply)
+       bucket                      = "example-mobile-qa-builds"
+       force_destroy               = false

+       cors_rule (known after apply)
    }

Plan: 1 to add, 3 to change, 0 to destroy.

Warning: Deprecated Resource

  with cloudflare_access_application.monitoring,
  on access.tf line 2, in resource "cloudflare_access_application" "monitoring":
   2: resource "cloudflare_access_application" "monitoring" {

(and 216 more similar warnings elsewhere)
```
</details>

:memo: Plan generated in [Terraform Plan #1](https://github.com/example/infra/actions/runs/1)
