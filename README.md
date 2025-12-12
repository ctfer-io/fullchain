<div align="center">
  <h1>Fullchain</h1>
  <a href=""><img src="https://img.shields.io/github/license/ctfer-io/fullchain?style=for-the-badge" alt="License"></a>
</div>

The *Fullchain* is an umbrella project that sacrifice the independent deployability of CTFer.io's Micro Service Architecture in profit of a ready-to-use CTF (Capture The Flag) platform.
It deploys it on Kubernetes with no adherence for the host platform (i.e., on-premise with [L3](https://github.com/ctfer-io/l3), AWS with [Backup](https://github.com/ctfer-io/backup), or any other).

Its purpose is to help deploy production-like environment that the community might end up deploying themselves, for test purposes, demonstrations, or SaaS work on sponsored events.

It notably contains [CTFd](https://github.com/ctfd/ctfd), [Chall-Manager](https://github.com/ctfer-io/chall-manager), and the [CTFd-Chall-Manager](https://github.com/ctfer-io/ctfd-chall-manager) plugin already configured, along with the [Monitoring](https://github.com/ctfer-io/monitoring) stack.

> [!CAUTION]
>
> This component is an **internal** work mostly used for development purposes.
> It is used for production purposes too, i.e. on Capture The Flag events.
>
> Nonetheless, **we do not include it in the repositories we are actively maintaining**.

## Table of Contents

- [Getting Started](#getting-started)
- [Advanced Setup](#advanced-setup)

## Getting Started

To get started with the Fullchain project and deploy it inside your cluster, follow these steps:

For now, the `github.com/ctfer-io/ctfer` dependency is **private** and needs some tricks to use it.

1. **Set up SSH Agent and Add SSH Key:**

```bash
eval "$(ssh-agent -s)"
ssh-add ~/.ssh/id_ed25519
export GOPRIVATE=github.com/ctfer-io/ctfer
```

2. **Download all dependencies:**

```bash
go mod tidy
```

3. **Initialize Pulumi Stack:**

```bash
pulumi login --local
pulumi stack init prod
```

4. **Configure Dedicated Challenges Cluster (Optional):**

If you want to configure a dedicated cluster for challenges, run:

```bash
pulumi config set --secret chall-kube-config "$(cat ~/.kube/config-challenge)"
```

5. **Configure CTFd**

```bash
# Configure HTTPS access
export PULUMI_CONFIG_PASSPHRASE="xx"
cat path/to/ctfd.crt | pulumi config set --secret crt
cat path/to/ctfd.key | pulumi config set --secret key

# Configure Ingress route
pulumi config set hostname ctfd.yourdomain

# Configure the replicas and workers
pulumi config set ctfd-replicas 3
pulumi config set ctfd-workers 3
```

6. **Deploy the Stack:**

```bash
pulumi up -y
```

## Advanced Setup

### Air-Gap Environment

For air-gap environments, you need to download all images and upload them into your registry before deployment. You can use [Hauler](https://docs.hauler.dev/) to download and push all images at once.

The following actions must be performed before the `pulumi up -y`.

1. **Navigate to the Hack Directory:**

```bash
cd hack
```

2. **Sync Images with Hauler:**

```bash
hauler store sync -f chaine-totale.yml
```

3. **Copy Images to Your Registry:**

```bash
hauler store copy registry://your-registry:5000
```

4. **Configure the Registry to Use on Your Stack:**

```bash
pulumi config set registry your-registry:5000
```

### Without CTFer-L3

If you are not using the [L3](https://github.com/ctfer-io/ctfer-l3), you need to install some Helm charts manually:
- [Longhorn](https://longhorn.io/): to enable persistent storage on chall-manager, ctfd's database... ;
- [Traefik](https://traefik.io): as ingress controller to route HTTPS traffic from outside ;
- [Cilium](https://docs.cilium.io/): as internal CNI;
- [MetalLB](https://metallb.io/): as Load Balancer used by Traefik.
- [Postres-Operator](https://github.com/zalando/postgres-operator): to deploy PostgreSQL HA

The following commands can be different depending on your Kubernetes setup (if you are using Talos based cluster):

```bash
# Install Cilium CNI
helm repo add cilium https://helm.cilium.io/
helm repo update
helm install cilium cilium/cilium --version 1.17.5 --namespace kube-system

# Install Longhorn
helm repo add longhorn https://charts.longhorn.io
helm repo update
helm install longhorn longhorn/longhorn --namespace longhorn-system --create-namespace --version 1.9.0

# Install MetalLB
helm repo add metallb https://metallb.github.io/metallb
helm install metallb metallb/metallb --namespace metallb-system --set speaker.frr.enabled=false --create-namespace --version 0.14.9

# Configure Metallb addresses pool EDIT as your need
cat <<EOF > ipaddresspool.yml
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: loadbalancer-pool
  namespace: metallb-system
spec:
  addresses:
  - 10.17.12.200/32
  autoAssign: true
EOF

kubectl apply -f ipaddresspool.yml

# Install Traefik
helm repo add traefik https://traefik.github.io/charts
helm repo update

cat <<EOF > traefik-values.yml
ports:
  web:
    redirections:
      entryPoint:
        scheme: https
        to: websecure
  websecure:
    asDefault: true
providers:
  kubernetesCRD:
    enabled: true
  kubernetesIngress:
    allowCrossNamespace: true
EOF

helm install traefik traefik/traefik --namespace ingress-controller --create-namespace --version 35.2.0 -f traefik-values.yml

# Install cnpg operator
helm repo add cnpg https://cloudnative-pg.github.io/charts
helm upgrade --install cnpg \
  --namespace cnpg-system \
  --create-namespace \
  cnpg/cloudnative-pg

```

## Known limitations

Due to the maturity of the fullchain project, some configurations are not yet easily customizable.
To use this project correctly, you need to:
- Install the cnpg operator in the `cnpg-system` namespace
- Install the ingress controller in the `ingress-controller` namespace (only Traefik has been tested so far, but nginx should also work)
- Install cilium as CNI (+ enable Hubble for debugging, not necessary for production)
- Use a CTFd image with `psycopg2-binary` package (or create yours with `ctferio/ctfd:0.8.0` or later as base image)