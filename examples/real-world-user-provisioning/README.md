# Real-World Example: User → Computer → Exchange Online Mailbox

A runnable version of the
[Real-World guide](../../docs/guides/real-world-user-provisioning.md), refactored
so each object type is its own submodule. The root module
([main.tf](./main.tf)) only wires the modules together, which keeps the data flow
front and center.

```
real-world-user-provisioning/
├── main.tf                      # providers + module composition + outputs
├── variables.tf                 # connection + user inputs
└── modules/
    ├── ad-user/                 # New-ADUser           (default provider)
    ├── ad-computer/             # New-ADComputer       (default provider)
    └── exo-mailbox-email/       # Set-Mailbox          (powershell.exo alias)
```

## What it does

1. **`module.user`** creates an Active Directory user (disabled; a password is set
   out-of-band by your SSPR / onboarding process).
2. **`module.computer`** creates a workstation whose `ManagedBy` is the user's
   distinguished name — so it references `module.user`'s output and is created
   after it.
3. **`module.mailbox_email`** sets the user's Exchange Online primary SMTP address.
   It declares an `powershell.exo` provider slot (`configuration_aliases`) that the
   root binds to the Exchange Online configuration.

`module.computer` and `module.mailbox_email` both depend only on `module.user`, so
Terraform may create them in either order.

## Run it

```bash
export TF_VAR_ad_admin_password="…"
terraform init
terraform apply \
  -var 'first_name=Jane' \
  -var 'last_name=Doe' \
  -var 'domain=contoso.com' \
  -var 'domain_controller=dc01.contoso.com' \
  -var 'base_ou=OU=Staff,DC=contoso,DC=com' \
  -var 'ad_admin_username=svc-deploy' \
  -var 'exo_app_id=00000000-0000-0000-0000-000000000000' \
  -var 'exo_cert_thumbprint=ABCDEF0123456789ABCDEF0123456789ABCDEF01' \
  -var 'exo_organization=contoso.onmicrosoft.com'
```

This example targets real Active Directory and Exchange Online environments and is
intended as a reference for structure; adapt the connection details and OU to your
tenant before applying.
