package services

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	netwv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/networking/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	cmcommon "github.com/ctfer-io/chall-manager/deploy/common"
	challmanager "github.com/ctfer-io/chall-manager/deploy/services"
	cmparts "github.com/ctfer-io/chall-manager/deploy/services/parts"
	ctfer "github.com/ctfer-io/ctfer/services"
	ctfercommon "github.com/ctfer-io/ctfer/services/common"
	"github.com/ctfer-io/fullchain/services/parts"
	monitoring "github.com/ctfer-io/monitoring/services"
)

type (
	Fullchain struct {
		pulumi.ResourceState

		mon *monitoring.Monitoring
		ns  *parts.Namespace

		oci       *parts.OCI
		cmToReg   *netwv1.NetworkPolicy
		regFromCM *netwv1.NetworkPolicy

		cm         *challmanager.ChallManager
		cmFromCTFd *netwv1.NetworkPolicy

		ctfer      *ctfer.CTFer
		ctfdToOTel *netwv1.NetworkPolicy
		ctfdToCM   *netwv1.NetworkPolicy
		expCTFd    *corev1.Service
		exposeCTFd *netwv1.NetworkPolicy

		OCINodePort  pulumi.IntOutput
		CTFdNodePort pulumi.IntOutput
		URL          pulumi.StringOutput
	}

	FullchainArgs struct {
		Monitoring   *MonitoringArgs
		ChallManager *ChallManagerArgs
		CTFer        *CTFerArgs
		OCI          *OCIArgs

		IngressNamespace pulumi.StringInput
		IngressLabels    pulumi.StringMapInput
		Registry         pulumi.StringInput
		registry         pulumi.StringOutput
	}

	MonitoringArgs struct {
		StorageClassName pulumi.StringInput
		StorageSize      pulumi.StringInput
		PVCAccessModes   pulumi.StringArrayInput

		ColdExtract bool
	}

	ChallManagerArgs struct {
		Tag            pulumi.StringPtrInput
		LogLevel       pulumi.StringInput
		EtcdReplicas   pulumi.IntPtrInput
		Replicas       pulumi.IntPtrInput
		JanitorCron    pulumi.StringInput
		JanitorTicker  pulumi.StringInput
		JanitorMode    cmparts.JanitorMode
		PVCAccessModes pulumi.StringArrayInput
		PVCStorageSize pulumi.StringInput
		Kubeconfig     pulumi.StringInput
		Requests       pulumi.StringMapInput
		Limits         pulumi.StringMapInput
		Envs           pulumi.StringMapInput
		Swagger        bool
		OCIInsecure    bool

		// Romeo args are not binded as the fullchain is not supposed to provide code-level measurements

		// TODO APIServerTemplate should be binded globally, as shared between multiple components. Tho, their current usage hardcode a name
	}

	CTFerArgs struct {
		Platform *PlatformArgs
		DB       *DBArgs
		Cache    *CacheArgs

		Expose bool
	}

	// PlatformArgs is the encapsulation of platform-specific arguments.
	// Current choice is CTFd.
	PlatformArgs struct {
		Image pulumi.StringInput

		Crt         pulumi.StringInput
		Key         pulumi.StringInput
		StorageSize pulumi.StringInput
		Workers     pulumi.IntInput
		Replicas    pulumi.IntInput
		Requests    pulumi.StringMapInput
		Limits      pulumi.StringMapInput

		StorageClass pulumi.StringInput

		// PVCAccessModes defines the access modes supported by the PVC.
		PVCAccessModes pulumi.StringArrayInput

		Hostname           pulumi.StringInput
		IngressAnnotations pulumi.StringMapInput
	}

	// DBArgs is the encapsulation of platform-specific arguments.
	// Current choice is PostgreSQL with CNPG.
	DBArgs struct {
		StorageClassName pulumi.StringInput

		OperatorNamespace pulumi.StringInput

		Replicas pulumi.IntInput
	}

	// CacheArgs is the encapsulation of platform-specific arguments.
	// Current choice is Redis.
	CacheArgs struct {
		Replicas pulumi.IntInput
	}

	// OCIArgs is the encapsulation of internal OCI registry.
	// It could be used to host challenges containers or Chall-Manager scenarios.
	OCIArgs struct {
		Username pulumi.StringInput
		Password pulumi.StringInput

		WithInsideRegistry bool
		ClusterIP          pulumi.StringInput
		PVCStorageSize     pulumi.StringInput
	}

	OTelArgs struct {
		// The OpenTelemetry service name to export signals with.
		ServiceName pulumi.StringPtrInput

		// The OpenTelemetry Collector (OTLP through gRPC) endpoint to send signals to.
		Endpoint pulumi.StringInput

		// Set to true if the endpoint is insecure (i.e. no TLS).
		Insecure bool
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
	if args.Monitoring == nil {
		args.Monitoring = &MonitoringArgs{}
	}
	if args.ChallManager == nil {
		args.ChallManager = &ChallManagerArgs{}
	}
	if args.CTFer == nil {
		args.CTFer = &CTFerArgs{}
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

	// Default CTFer parameters (high-level settings, all others will be check by CTFer directly)
	if args.CTFer.Platform.StorageSize == nil {
		args.CTFer.Platform.StorageSize = pulumi.String("10Gi")
	}
	if args.CTFer.Platform.IngressAnnotations == nil {
		args.CTFer.Platform.IngressAnnotations = pulumi.ToStringMap(map[string]string{
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
		})
	}

	return args
}

func (fch *Fullchain) provision(ctx *pulumi.Context, args *FullchainArgs, opts ...pulumi.ResourceOption) (err error) {
	fch.mon, err = monitoring.NewMonitoring(ctx, "monitoring", &monitoring.MonitoringArgs{
		Registry:         args.registry,
		StorageClassName: args.Monitoring.StorageClassName,
		StorageSize:      args.Monitoring.StorageSize,
		ColdExtract:      args.Monitoring.ColdExtract,
		PVCAccessModes:   args.Monitoring.PVCAccessModes,
	}, opts...)
	if err != nil {
		return
	}

	fch.ns, err = parts.NewNamespace(ctx, "fullchain", &parts.NamespaceArgs{
		Name: pulumi.Sprintf("fullchain-%s", ctx.Stack()),
	}, opts...)
	if err != nil {
		return
	}

	fch.cm, err = challmanager.NewChallManager(ctx, "chall-manager", &challmanager.ChallManagerArgs{
		Tag:                   args.ChallManager.Tag,
		Registry:              args.registry,
		LogLevel:              args.ChallManager.LogLevel,
		Namespace:             fch.ns.Name,
		EtcdReplicas:          args.ChallManager.EtcdReplicas,
		Replicas:              args.ChallManager.Replicas,
		JanitorCron:           args.ChallManager.JanitorCron,
		JanitorTicker:         args.ChallManager.JanitorTicker,
		JanitorMode:           args.ChallManager.JanitorMode,
		PVCAccessModes:        args.ChallManager.PVCAccessModes,
		PVCStorageSize:        args.ChallManager.PVCStorageSize,
		RomeoClaimName:        nil,
		Kubeconfig:            args.ChallManager.Kubeconfig,
		Requests:              args.ChallManager.Requests,
		Limits:                args.ChallManager.Limits,
		Envs:                  args.ChallManager.Envs,
		CmToApiServerTemplate: nil,
		Swagger:               args.ChallManager.Swagger,
		Expose:                false, // Do not expose on a NodePort, it is for independent deployability
		Otel: &cmcommon.OtelArgs{
			Endpoint:    fch.mon.OTEL.Endpoint,
			ServiceName: pulumi.String(ctx.Stack()),
			Insecure:    true, // XXX @pandatix fix this shit
		},
		OCIInsecure: args.ChallManager.OCIInsecure,
		OCIUsername: args.OCI.Username,
		OCIPassword: args.OCI.Password,
	}, opts...)
	if err != nil {
		return
	}

	if args.OCI.WithInsideRegistry {
		fch.oci, err = parts.NewOCI(ctx, "inside", &parts.OCIArgs{
			Registry:       args.registry,
			Namespace:      fch.ns.Name,
			ClusterIP:      args.OCI.ClusterIP,
			PVCStorageSize: args.OCI.PVCStorageSize,
			Username:       args.OCI.Username,
			Password:       args.OCI.Password,
			OTel: &cmcommon.OtelArgs{
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
									MatchLabels: fch.oci.PodLabels,
								},
							},
						},
						Ports: netwv1.NetworkPolicyPortArray{
							netwv1.NetworkPolicyPortArgs{
								Port:     parseEndpoint(fch.oci.Endpoint),
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
					MatchLabels: fch.oci.PodLabels,
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
								Port:     parseEndpoint(fch.oci.Endpoint),
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
		Namespace: fch.ns.Name,
		Platform: &ctfer.PlatformArgs{
			Image:              args.CTFer.Platform.Image,
			ChallManagerURL:    pulumi.Sprintf("http://%s", fch.cm.Endpoint), // The CM plugin uses the HTTP gateway
			Crt:                args.CTFer.Platform.Crt,
			Key:                args.CTFer.Platform.Key,
			StorageSize:        args.CTFer.Platform.StorageSize,
			Workers:            args.CTFer.Platform.Workers,
			Replicas:           args.CTFer.Platform.Replicas,
			Requests:           args.CTFer.Platform.Requests,
			Limits:             args.CTFer.Platform.Limits,
			StorageClassName:   args.CTFer.Platform.StorageClass,
			PVCAccessModes:     args.CTFer.Platform.PVCAccessModes,
			Hostname:           args.CTFer.Platform.Hostname,
			IngressAnnotations: args.CTFer.Platform.IngressAnnotations,
		},
		DB: &ctfer.DBArgs{
			StorageClassName:  args.CTFer.DB.StorageClassName,
			OperatorNamespace: args.CTFer.DB.OperatorNamespace,
			Replicas:          args.CTFer.DB.Replicas,
		},
		Cache: &ctfer.CacheArgs{
			Replicas: args.CTFer.Cache.Replicas,
		},
		ChartsRepository: args.registry, // XXX having a chart repositry sucks, we have an OCI one and that's it
		ImagesRepository: args.registry,
		IngressNamespace: args.IngressNamespace,
		IngressLabels:    args.IngressLabels,
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
	if args.CTFer.Expose {
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
	if fch.oci != nil {
		fch.OCINodePort = fch.oci.NodePort
	}
	if fch.expCTFd != nil {
		fch.CTFdNodePort = fch.expCTFd.Spec.Ports().Index(pulumi.Int(0)).NodePort().Elem()
	}

	return ctx.RegisterResourceOutputs(fch, pulumi.Map{
		"url":           fch.URL,
		"oci.nodeport":  fch.OCINodePort,
		"ctfd.nodeport": fch.CTFdNodePort,
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
