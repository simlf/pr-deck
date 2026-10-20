<!-- dflook/terraform-github-actions {"type": "plan", "path": "terraform/ovh", "workspace": "production-par", "plan_modifier": "", "variables": null} -->
Terraform plan in __terraform/ovh__ in the __production-par__ workspace
<details open>
<summary>Plan: 2 to add, 1 to change, 2 to destroy.</summary>

```hcl
Terraform used the selected providers to generate the following execution
plan. Resource actions are indicated with the following symbols:
  + create
  ~ update in-place
  - destroy
-/+ destroy and then create replacement

Terraform will perform the following actions:

  # data.ovh_cloud_project_database_ip_restrictions.redis will be read during apply
  # (depends on a resource or a module with changes pending)
 <= data "ovh_cloud_project_database_ip_restrictions" "redis" {
      + id = (known after apply)
    }

  # module.redis["cache"].ovh_cloud_project_database.this must be replaced
-/+ resource "ovh_cloud_project_database" "this" {
      ~ flavor = "db1-4" -> "db1-7" # forces replacement
    }

  # ovh_cloud_project_database.opensearch_clusters["main"] will be created
  + resource "ovh_cloud_project_database" "opensearch_clusters" {
      + engine = "opensearch"
    }

  # ovh_cloud_project_database_user.app will be updated in-place
  ~ resource "ovh_cloud_project_database_user" "app" {
      ~ roles = [
          + "read",
        ]
    }

  # ovh_cloud_project_kube_nodepool.old will be destroyed
  - resource "ovh_cloud_project_kube_nodepool" "old" {
      - name = "old"
    }

  # ovh_domain_zone_record.lb["api"] (deposed object 1a2b3c4d) will be destroyed
  - resource "ovh_domain_zone_record" "lb" {
      - target = "1.2.3.4"
    }

  # ovh_cloud_project_database_ip_restrictions.this is tainted, so must be replaced
-/+ resource "ovh_cloud_project_database_ip_restrictions" "this" {
    }

  # ovh_iam_policy.ops has moved to ovh_iam_policy.sre
    resource "ovh_iam_policy" "sre" {
        name = "sre"
    }

  # ovh_vrack_cloudproject.vrack will be created
  + resource "ovh_vrack_cloudproject" "vrack" {
      + project_id = "abc"
    }

Plan: 2 to add, 1 to change, 2 to destroy.
```
</details>
:mag: View full plan log: https://github.com/example/infra/actions/runs/1
