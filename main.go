package main

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	netwv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/networking/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"

	// romeoenv "github.com/ctfer-io/romeo/environment/deploy/parts"

	"github.com/ctfer-io/chall-manager/deploy/common"
	challmanager "github.com/ctfer-io/chall-manager/deploy/services"
	ctfer "github.com/ctfer-io/ctfer/services"
	"github.com/ctfer-io/fullchain/parts"
	monitoring "github.com/ctfer-io/monitoring/services"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg, err := InitConfig(ctx)
		if err != nil {
			return err
		}

		// Initialize providers or any transform that must be propagated
		// through the execution graph.
		opts := []pulumi.ResourceOption{}

		// => Monitoring
		monConf := &monitoring.MonitoringArgs{
			ColdExtract:      cfg.ColdExtract,
			StorageClassName: pulumi.String("longhorn"),
		}
		if cfg.Registry != "" {
			monConf.Registry = pulumi.String(cfg.Registry)
		}
		mon, err := monitoring.NewMonitoring(ctx, "monitoring", monConf, opts...)
		if err != nil {
			return err
		}

		// => Namespace to deploy the platform
		ns, err := parts.NewNamespace(ctx, "ctf", &parts.NamespaceArgs{
			Name: pulumi.String("fullchain"),
		}, opts...)
		if err != nil {
			return err
		}

		// => Chall-Manager
		cmConf := &challmanager.ChallManagerArgs{
			LogLevel: pulumi.String("info"),
			Requests: pulumi.ToStringMap(map[string]string{
				"memory": "2Gi",
				"cpu":    "1.0",
			}),
			PVCStorageSize: pulumi.String("10Gi"),
			Tag:            pulumi.String("v0.4.5"),

			Namespace: ns.Name,
			Otel: &common.OtelArgs{
				Endpoint:    mon.OTEL.Endpoint,
				ServiceName: pulumi.String(ctx.Stack()),
				Insecure:    true, // XXX @pandatix fix this shit
			},
		}

		if cfg.Registry != "" {
			cmConf.Registry = pulumi.String(cfg.Registry)
		}
		cm, err := challmanager.NewChallManager(ctx, "chall-manager", cmConf, opts...)
		if err != nil {
			return err
		}

		// // => CTFer/CTFd
		ctfdConf := &ctfer.CTFerArgs{
			Namespace:       ns.Name,
			Hostname:        cfg.CTFdHostname,
			CTFdImage:       pulumi.String("ctferio/ctfd:3.7.7-0.3.4"),
			CTFdCrt:         cfg.CTFdCrt,
			CTFdKey:         cfg.CTFdKey,
			CTFdStorageSize: pulumi.String("10Gi"),
			ChallManagerUrl: pulumi.Sprintf("http://%s/api/v1", cm.Endpoint),

			// => Ingress-related configuration
			IngressNamespace: pulumi.String("ingress-controller"),
			IngressLabels: pulumi.ToStringMap(map[string]string{
				"app.kubernetes.io/name": "traefik",
			}),
		}

		// Air-gapped
		if cfg.Registry != "" {
			ctfdConf.ImagesRepository = pulumi.String(cfg.Registry)
			ctfdConf.ChartsRepository = pulumi.Sprintf("oci://%s/hauler", cfg.Registry)
		}

		ctfer, err := ctfer.NewCTFer(ctx, "platform", ctfdConf, opts...)
		if err != nil {
			return err
		}

		if _, err := netwv1.NewNetworkPolicy(ctx, "ctfd-to-cm", &netwv1.NetworkPolicyArgs{
			Metadata: metav1.ObjectMetaArgs{
				Namespace: ns.Name,
				Labels: pulumi.StringMap{
					"app.kubernetes.io/part-of": pulumi.String("fullchain"),
					"ctfer.io/stack-name":       pulumi.String(ctx.Stack()),
				},
			},
			Spec: netwv1.NetworkPolicySpecArgs{
				PolicyTypes: pulumi.ToStringArray([]string{
					"Egress",
				}),
				PodSelector: metav1.LabelSelectorArgs{
					MatchLabels: ctfer.PodLabels,
				},
				Egress: netwv1.NetworkPolicyEgressRuleArray{
					netwv1.NetworkPolicyEgressRuleArgs{
						To: netwv1.NetworkPolicyPeerArray{
							netwv1.NetworkPolicyPeerArgs{
								NamespaceSelector: metav1.LabelSelectorArgs{
									MatchLabels: pulumi.StringMap{
										"kubernetes.io/metadata.name": ns.Name,
									},
								},
								PodSelector: metav1.LabelSelectorArgs{
									MatchLabels: cm.PodLabels,
								},
							},
						},
						Ports: netwv1.NetworkPolicyPortArray{
							netwv1.NetworkPolicyPortArgs{
								Port:     parsePort(cm.Endpoint),
								Protocol: pulumi.String("TCP"),
							},
						},
					},
				},
			},
		}, opts...); err != nil {
			return err
		}

		if _, err := netwv1.NewNetworkPolicy(ctx, "cm-from-ctfd", &netwv1.NetworkPolicyArgs{
			Metadata: metav1.ObjectMetaArgs{
				Namespace: ns.Name,
				Labels: pulumi.StringMap{
					"app.kubernetes.io/part-of": pulumi.String("fullchain"),
					"ctfer.io/stack-name":       pulumi.String(ctx.Stack()),
				},
			},
			Spec: netwv1.NetworkPolicySpecArgs{
				PolicyTypes: pulumi.ToStringArray([]string{
					"Ingress",
				}),
				PodSelector: metav1.LabelSelectorArgs{
					MatchLabels: cm.PodLabels,
				},
				Ingress: netwv1.NetworkPolicyIngressRuleArray{
					netwv1.NetworkPolicyIngressRuleArgs{
						From: netwv1.NetworkPolicyPeerArray{
							netwv1.NetworkPolicyPeerArgs{
								NamespaceSelector: metav1.LabelSelectorArgs{
									MatchLabels: pulumi.StringMap{
										"kubernetes.io/metadata.name": ns.Name,
									},
								},
								PodSelector: metav1.LabelSelectorArgs{
									MatchLabels: ctfer.PodLabels,
								},
							},
						},
						Ports: netwv1.NetworkPolicyPortArray{
							netwv1.NetworkPolicyPortArgs{
								Port:     parsePort(cm.Endpoint),
								Protocol: pulumi.String("TCP"),
							},
						},
					},
				},
			},
		}, opts...); err != nil {
			return err
		}

		ctx.Export("namespace", ns.Name)
		ctx.Export("chall-manager-endpoint", cm.Endpoint)
		ctx.Export("url", ctfer.URL)
		return nil
	})
}

type Config struct {
	ColdExtract     bool
	ChallKubeConfig pulumi.StringInput
	CTFdCrt         pulumi.StringInput
	CTFdKey         pulumi.StringInput
	CTFdHostname    pulumi.StringInput
	Registry        string
}

func InitConfig(ctx *pulumi.Context) (*Config, error) {
	cfg := config.New(ctx, "")
	return &Config{
		ColdExtract:  cfg.GetBool("cold-extract"),
		CTFdCrt:      cfg.RequireSecret("ctfd-crt"),
		CTFdKey:      cfg.RequireSecret("ctfd-key"),
		CTFdHostname: pulumi.String(cfg.Require("ctfd-hostname")),
		Registry:     cfg.Get("registry"),
	}, nil
}

// parsePort cuts the input endpoint to return its port.
// Example: some.thing:port -> port
func parsePort(edp pulumi.StringInput) pulumi.IntOutput {
	return edp.ToStringOutput().ApplyT(func(edp string) (int, error) {
		_, pStr, _ := strings.Cut(edp, ":")
		p, err := strconv.Atoi(pStr)
		if err != nil {
			return 0, errors.Wrapf(err, "parsing endpoint %s for port", edp)
		}
		return p, nil
	}).(pulumi.IntOutput)
}

// parseURLPort parses the input endpoint formatted as a URL to return its port.
// Example: http://some.thing:port -> port
func parseURLPort(edp pulumi.StringOutput) pulumi.IntOutput {
	return edp.ToStringOutput().ApplyT(func(edp string) (int, error) {
		u, err := url.Parse(edp)
		if err != nil {
			return 0, errors.Wrapf(err, "parsing endpoint %s as a URL", edp)
		}
		p, err := strconv.Atoi(u.Port())
		if err != nil {
			return 0, errors.Wrapf(err, "parsing endpoint %s for port", edp)
		}
		return p, nil
	}).(pulumi.IntOutput)
}
