## Dev setup

```bash
eval "$(ssh-agent -s)"
ssh-add ~/.ssh/id_ed25519
export GOPRIVATE=github.com/ctfer-io/monitoring,github.com/ctfer-io/ctfer
go mod tidy

# Start Pulumi program
export PULUMI_CONFIG_PASSPHRASE=""
pulumi stack init prod
pulumi up -y
```
