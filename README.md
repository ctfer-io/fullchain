## Dev setup

```bash
eval "$(ssh-agent -s)"
ssh-add ~/.ssh/id_ed25519
export GOPRIVATE=github.com/ctfer-io/monitoring,github.com/ctfer-io/ctfer
go mod tidy

# Start Pulumi program
export KUBECONFIG="~/.kube/config-dev1"
export PULUMI_CONFIG_PASSPHRASE=""
pulumi stack init prod
pulumi config set --secret chall-kube-config "$(cat ~/.kube/config-dev2)"

cat ~/ctfer-io/ctfer/certs/ctfd.crt | pulumi config set --secret ctfd-crt
cat ~/ctfer-io/ctfer/certs/ctfd.key | pulumi config set --secret ctfd-key

pulumi up -y
```
