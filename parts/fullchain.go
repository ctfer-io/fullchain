package parts

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	netwv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/networking/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/ctfer-io/chall-manager/deploy/common"
	challmanager "github.com/ctfer-io/chall-manager/deploy/services"
	ctfer "github.com/ctfer-io/ctfer/services"
	ctfercommon "github.com/ctfer-io/ctfer/services/common"
	monitoring "github.com/ctfer-io/monitoring/services"
)

type (
	Fullchain struct {
		pulumi.ResourceState

		mon *monitoring.Monitoring
		ns  *Namespace

		reg       *Registry
		cmToReg   *netwv1.NetworkPolicy
		regFromCM *netwv1.NetworkPolicy

		cm         *challmanager.ChallManager
		cmFromCTFd *netwv1.NetworkPolicy

		ctfer      *ctfer.CTFer
		ctfdToOTel *netwv1.NetworkPolicy
		ctfdToCM   *netwv1.NetworkPolicy
		expCTFd    *corev1.Service
		exposeCTFd *netwv1.NetworkPolicy

		RegistryNodePort pulumi.IntOutput
		CTFdNodePort     pulumi.IntOutput
		URL              pulumi.StringOutput
	}

	FullchainArgs struct {
		// Monitoring

		ColdExtract bool

		// Registry

		Registry pulumi.StringInput
		registry pulumi.StringOutput

		WithInsideRegistry bool
		RegistryClusterIP  pulumi.StringPtrInput
		OCIUsername        pulumi.StringInput
		OCIPassword        pulumi.StringInput

		// Chall-Manager

		ChallKubeConfig pulumi.StringInput

		// CTFer

		Crt      pulumi.StringInput
		Key      pulumi.StringInput
		Hostname pulumi.StringInput
		Expose   bool

		// Common

		StorageClassName pulumi.StringInput
	}
)

func NewFullchain(
	ctx *pulumi.Context,
	name string,
	args *FullchainArgs,
	opts ...pulumi.ResourceOption,
) (*Fullchain, error) {
	fch := &Fullchain{}

	args = fch.defaults(args)
	if err := ctx.RegisterComponentResource("ctfer-io:fullchain:fullchain", name, fch, opts...); err != nil {
		return nil, err
	}
	opts = append(opts, pulumi.Parent(fch))
	if err := fch.provision(ctx, args, opts...); err != nil {
		return nil, err
	}
	if err := fch.outputs(ctx); err != nil {
		return nil, err
	}
	return fch, nil
}

func (fch *Fullchain) defaults(args *FullchainArgs) *FullchainArgs {
	if args == nil {
		args = &FullchainArgs{}
	}

	// Define private registry if any
	args.registry = pulumi.String("").ToStringOutput()
	if args.Registry != nil {
		args.registry = args.Registry.ToStringPtrOutput().ApplyT(func(in *string) string {
			// No private registry -> defaults to Docker Hub
			if in == nil {
				return ""
			}

			str := *in
			// If one set, make sure it ends with one '/'
			if str != "" && !strings.HasSuffix(str, "/") {
				str = str + "/"
			}
			return str
		}).(pulumi.StringOutput)
	}

	return args
}

func (fch *Fullchain) provision(ctx *pulumi.Context, args *FullchainArgs, opts ...pulumi.ResourceOption) (err error) {
	fch.mon, err = monitoring.NewMonitoring(ctx, "monitoring", &monitoring.MonitoringArgs{
		Registry:         args.registry,
		StorageClassName: args.StorageClassName,
		ColdExtract:      args.ColdExtract,
	}, opts...)
	if err != nil {
		return
	}

	fch.ns, err = NewNamespace(ctx, "ctf", &NamespaceArgs{
		Name: pulumi.String("fullchain"),
	}, opts...)
	if err != nil {
		return
	}

	fch.cm, err = challmanager.NewChallManager(ctx, "chall-manager", &challmanager.ChallManagerArgs{
		Tag:            pulumi.String("v0.5.3"),
		Registry:       args.registry,
		LogLevel:       pulumi.String("info"),
		Namespace:      fch.ns.Name,
		Kubeconfig:     args.ChallKubeConfig,
		PVCStorageSize: pulumi.String("10Gi"),
		Requests: pulumi.ToStringMap(map[string]string{
			"memory": "2Gi",
			"cpu":    "1.0",
		}),
		OCIInsecure: args.WithInsideRegistry, // within the cluster, is insecure (no SM for now). Else we force secure mode
		OCIUsername: args.OCIUsername,
		OCIPassword: args.OCIPassword,
		Otel: &common.OtelArgs{
			Endpoint:    fch.mon.OTEL.Endpoint,
			ServiceName: pulumi.String(ctx.Stack()),
			Insecure:    true, // XXX @pandatix fix this shit
		},
	}, opts...)
	if err != nil {
		return
	}

	if args.WithInsideRegistry {
		fch.reg, err = NewRegistry(ctx, "inside", &RegistryArgs{
			Registry:  args.registry,
			ClusterIP: args.RegistryClusterIP,
			Namespace: fch.ns.Name,
			Otel: &common.OtelArgs{
				Endpoint:    fch.mon.OTEL.Endpoint,
				ServiceName: pulumi.String(ctx.Stack()),
				Insecure:    true, // XXX @pandatix fix this shit
			},
		}, opts...)
		if err != nil {
			return
		}

		// Enable Chall-Manager to reach the registry
		fch.cmToReg, err = netwv1.NewNetworkPolicy(ctx, "cm-to-registry", &netwv1.NetworkPolicyArgs{
			Metadata: metav1.ObjectMetaArgs{
				Namespace: fch.ns.Name,
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
					MatchLabels: fch.cm.PodLabels,
				},
				Egress: netwv1.NetworkPolicyEgressRuleArray{
					netwv1.NetworkPolicyEgressRuleArgs{
						To: netwv1.NetworkPolicyPeerArray{
							netwv1.NetworkPolicyPeerArgs{
								NamespaceSelector: metav1.LabelSelectorArgs{
									MatchLabels: pulumi.StringMap{
										"kubernetes.io/metadata.name": fch.ns.Name,
									},
								},
								PodSelector: metav1.LabelSelectorArgs{
									MatchLabels: fch.reg.PodLabels,
								},
							},
						},
						Ports: netwv1.NetworkPolicyPortArray{
							netwv1.NetworkPolicyPortArgs{
								Port:     parseEndpoint(fch.reg.Endpoint),
								Protocol: pulumi.String("TCP"),
							},
						},
					},
				},
			},
		}, opts...)
		if err != nil {
			return
		}

		fch.regFromCM, err = netwv1.NewNetworkPolicy(ctx, "registry-from-cm", &netwv1.NetworkPolicyArgs{
			Metadata: metav1.ObjectMetaArgs{
				Namespace: fch.ns.Name,
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
					MatchLabels: fch.reg.PodLabels,
				},
				Ingress: netwv1.NetworkPolicyIngressRuleArray{
					netwv1.NetworkPolicyIngressRuleArgs{
						From: netwv1.NetworkPolicyPeerArray{
							netwv1.NetworkPolicyPeerArgs{
								NamespaceSelector: metav1.LabelSelectorArgs{
									MatchLabels: pulumi.StringMap{
										"kubernetes.io/metadata.name": fch.ns.Name,
									},
								},
								PodSelector: metav1.LabelSelectorArgs{
									MatchLabels: fch.cm.PodLabels,
								},
							},
						},
						Ports: netwv1.NetworkPolicyPortArray{
							netwv1.NetworkPolicyPortArgs{
								Port:     parseEndpoint(fch.reg.Endpoint),
								Protocol: pulumi.String("TCP"),
							},
						},
					},
				},
			},
		})
		if err != nil {
			return
		}
	}

	fch.ctfer, err = ctfer.NewCTFer(ctx, "platform", &ctfer.CTFerArgs{
		Namespace:        fch.ns.Name,
		Hostname:         args.Hostname,
		CTFdImage:        pulumi.String("ctferio/ctfd:3.7.7-0.5.0"),
		Crt:              args.Crt,
		Key:              args.Key,
		StorageSize:      pulumi.String("10Gi"),
		ChallManagerURL:  pulumi.Sprintf("http://%s", fch.cm.Endpoint),
		IngressNamespace: pulumi.String("ingress-controller"),
		IngressLabels: pulumi.ToStringMap(map[string]string{
			"app.kubernetes.io/name": "traefik",
		}),
		Annotations: pulumi.ToStringMap(map[string]string{
			// The following serves when the LoadBalancer cannot give an ExternalIP
			// Example: on-prem Traefik
			"pulumi.com/skipAwait": "true",
			// The following serves when used along the AWS LoadBalancer Controller
			// from Kubernetes SIG-AWS.
			// "kubernetes.io/ingress.class":            "alb",
			// "alb.ingress.kubernetes.io/scheme":       "internet-facing",
			// "alb.ingress.kubernetes.io/target-type":  "ip",
			// "alb.ingress.kubernetes.io/listen-ports": `[{"HTTP":80}]`,
			// --- ExternalDNS annotations (creates DNS A record in Route 53) ---
			// "external-dns.alpha.kubernetes.io/hostname": "ctfd.ctfer.io",
			// "external-dns.alpha.kubernetes.io/ttl":      "60",
		}),
		OTel: &ctfercommon.OTelArgs{
			Endpoint:    fch.mon.OTEL.Endpoint,
			ServiceName: pulumi.String(ctx.Stack()),
			Insecure:    true, // XXX @pandatix fix this shit
		},
	}, opts...)
	if err != nil {
		return
	}

	fch.ctfdToCM, err = netwv1.NewNetworkPolicy(ctx, "ctfd-to-cm", &netwv1.NetworkPolicyArgs{
		Metadata: metav1.ObjectMetaArgs{
			Namespace: fch.ns.Name,
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
				MatchLabels: fch.ctfer.PodLabels,
			},
			Egress: netwv1.NetworkPolicyEgressRuleArray{
				netwv1.NetworkPolicyEgressRuleArgs{
					To: netwv1.NetworkPolicyPeerArray{
						netwv1.NetworkPolicyPeerArgs{
							NamespaceSelector: metav1.LabelSelectorArgs{
								MatchLabels: pulumi.StringMap{
									"kubernetes.io/metadata.name": fch.ns.Name,
								},
							},
							PodSelector: metav1.LabelSelectorArgs{
								MatchLabels: fch.cm.PodLabels,
							},
						},
					},
					Ports: netwv1.NetworkPolicyPortArray{
						netwv1.NetworkPolicyPortArgs{
							Port:     parseEndpoint(fch.cm.Endpoint),
							Protocol: pulumi.String("TCP"),
						},
					},
				},
			},
		},
	}, opts...)
	if err != nil {
		return
	}

	fch.cmFromCTFd, err = netwv1.NewNetworkPolicy(ctx, "cm-from-ctfd", &netwv1.NetworkPolicyArgs{
		Metadata: metav1.ObjectMetaArgs{
			Namespace: fch.ns.Name,
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
				MatchLabels: fch.cm.PodLabels,
			},
			Ingress: netwv1.NetworkPolicyIngressRuleArray{
				netwv1.NetworkPolicyIngressRuleArgs{
					From: netwv1.NetworkPolicyPeerArray{
						netwv1.NetworkPolicyPeerArgs{
							NamespaceSelector: metav1.LabelSelectorArgs{
								MatchLabels: pulumi.StringMap{
									"kubernetes.io/metadata.name": fch.ns.Name,
								},
							},
							PodSelector: metav1.LabelSelectorArgs{
								MatchLabels: fch.ctfer.PodLabels,
							},
						},
					},
					Ports: netwv1.NetworkPolicyPortArray{
						netwv1.NetworkPolicyPortArgs{
							Port:     parseEndpoint(fch.cm.Endpoint),
							Protocol: pulumi.String("TCP"),
						},
					},
				},
			},
		},
	}, opts...)
	if err != nil {
		return
	}

	fch.ctfdToOTel, err = netwv1.NewNetworkPolicy(ctx, "ctfd-to-otel", &netwv1.NetworkPolicyArgs{
		Metadata: metav1.ObjectMetaArgs{
			Namespace: fch.ns.Name,
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
				MatchLabels: fch.ctfer.PodLabels,
			},
			Egress: netwv1.NetworkPolicyEgressRuleArray{
				netwv1.NetworkPolicyEgressRuleArgs{
					Ports: netwv1.NetworkPolicyPortArray{
						netwv1.NetworkPolicyPortArgs{
							Port:     parseEndpoint(fch.mon.OTEL.Endpoint),
							Protocol: pulumi.String("TCP"),
						},
					},
				},
			},
		},
	}, opts...)
	if err != nil {
		return
	}

	// Expose CTFd using a NodePort
	if args.Expose {
		fch.expCTFd, err = corev1.NewService(ctx, "exposed-ctfd", &corev1.ServiceArgs{
			Metadata: metav1.ObjectMetaArgs{
				Namespace: fch.ns.Name,
				Labels: pulumi.StringMap{
					"app.kubernetes.io/part-of": pulumi.String("fullchain"),
					"ctfer.io/stack-name":       pulumi.String(ctx.Stack()),
				},
			},
			Spec: corev1.ServiceSpecArgs{
				Type:     pulumi.String("NodePort"),
				Selector: fch.ctfer.PodLabels,
				Ports: corev1.ServicePortArray{
					corev1.ServicePortArgs{
						Port: pulumi.Int(8000),
					},
				},
			},
		}, opts...)
		if err != nil {
			return
		}

		fch.exposeCTFd, err = netwv1.NewNetworkPolicy(ctx, "expose-ctfd", &netwv1.NetworkPolicyArgs{
			Metadata: metav1.ObjectMetaArgs{
				Namespace: fch.ns.Name,
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
					MatchLabels: fch.ctfer.PodLabels,
				},
				Ingress: netwv1.NetworkPolicyIngressRuleArray{
					netwv1.NetworkPolicyIngressRuleArgs{
						From: netwv1.NetworkPolicyPeerArray{
							netwv1.NetworkPolicyPeerArgs{
								IpBlock: netwv1.IPBlockArgs{
									Cidr: pulumi.String("0.0.0.0/0"),
								},
							},
						},
						Ports: netwv1.NetworkPolicyPortArray{
							netwv1.NetworkPolicyPortArgs{
								Port: fch.expCTFd.Spec.Ports().Index(pulumi.Int(0)).Port(),
							},
						},
					},
				},
			},
		}, opts...)
		if err != nil {
			return
		}
	}

	return
}

func (fch *Fullchain) outputs(ctx *pulumi.Context) error {
	fch.URL = fch.ctfer.URL
	if fch.reg != nil {
		fch.RegistryNodePort = fch.reg.NodePort
	}
	if fch.expCTFd != nil {
		fch.CTFdNodePort = fch.expCTFd.Spec.Ports().Index(pulumi.Int(0)).NodePort().Elem()
	}

	return ctx.RegisterResourceOutputs(fch, pulumi.Map{
		"url":               fch.URL,
		"registry.nodeport": fch.RegistryNodePort,
		"ctfd.nodeport":     fch.CTFdNodePort,
	})
}

// parseEndpoint cuts the input endpoint to return its port.
// Examples:
//   - some.thing:port -> port
//   - dns://some.thing:port -> port
func parseEndpoint(edp pulumi.StringInput) pulumi.IntOutput {
	return edp.ToStringOutput().ApplyT(func(edp string) (int, error) {
		// If it is a URL-formatted endpoint, parse it
		if u, err := url.Parse(edp); err == nil && u.Port() != "" {
			return parsePort(edp, u.Port())
		}

		// Else it should be a cuttable endpoint
		_, pStr, _ := strings.Cut(edp, ":")
		return parsePort(edp, pStr)
	}).(pulumi.IntOutput)
}

func parsePort(edp, port string) (int, error) {
	p, err := strconv.Atoi(port)
	if err != nil {
		return 0, errors.Wrapf(err, "parsing endpoint %s for port", edp)
	}
	return p, nil
}
