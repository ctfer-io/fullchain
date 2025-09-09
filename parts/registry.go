package parts

import (
	"errors"
	"strings"
	"sync"

	"github.com/ctfer-io/chall-manager/deploy/common"
	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	netwv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/networking/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"go.uber.org/multierr"
)

const (
	defaultPVCStorageSize = "2Gi"
)

type (
	// Registry is an un-authenticated OCI registry with OpenTelemetry support.
	// It could be used to store Chall-Manager scenarios within the cluster itself
	// rather than depending on an external service.
	// It could be exposed for maintenance purposes, but should not be used as-it
	// for production purposes.
	// For production-ready deployment, please look at https://distribution.github.io/distribution/about/deploying/
	Registry struct {
		pulumi.ResourceState

		pvc        *corev1.PersistentVolumeClaim
		dep        *appsv1.Deployment
		svc        *corev1.Service
		exposedNtp *netwv1.NetworkPolicy

		PodLabels pulumi.StringMapOutput
		Endpoint  pulumi.StringOutput
		NodePort  pulumi.IntOutput
	}

	RegistryArgs struct {
		// Registry define from where to fetch the Docker image.
		// If set empty, defaults to Docker Hub.
		// Authentication is not supported, please provide it as Kubernetes-level configuration.
		Registry pulumi.StringPtrInput
		registry pulumi.StringOutput

		// ClusterIP is an optional argument to hardcode the service cluster IP.
		// It is mostly used to bootstrap local/demo infrastructures on a single node.
		ClusterIP pulumi.StringPtrInput

		// Namespace in which to deploy the registry.
		Namespace pulumi.StringInput

		// PVCStorageSize enable to configure the storage size of the PVC the registry
		// will write into (OCI scenarios).
		// Default to 2Gi.
		// See https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#meaning-of-memory
		// for syntax.
		PVCStorageSize pulumi.StringInput
		pvcStorageSize pulumi.StringOutput

		Otel *common.OtelArgs
	}
)

func NewRegistry(ctx *pulumi.Context, name string, args *RegistryArgs, opts ...pulumi.ResourceOption) (*Registry, error) {
	reg := &Registry{}

	args = reg.defaults(args)
	if err := reg.check(args); err != nil {
		return nil, err
	}
	if err := ctx.RegisterComponentResource("ctfer-io:fullchain:registry", name, reg, opts...); err != nil {
		return nil, err
	}
	opts = append(opts, pulumi.Parent(reg))
	if err := reg.provision(ctx, args, opts...); err != nil {
		return nil, err
	}
	if err := reg.outputs(ctx); err != nil {
		return nil, err
	}
	return reg, nil
}

func (reg *Registry) defaults(args *RegistryArgs) *RegistryArgs {
	if args == nil {
		args = &RegistryArgs{}
	}

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

	args.pvcStorageSize = pulumi.String(defaultPVCStorageSize).ToStringOutput()
	if args.PVCStorageSize != nil {
		args.pvcStorageSize = args.PVCStorageSize.ToStringOutput().ApplyT(func(size string) string {
			if size == "" {
				return defaultPVCStorageSize
			}
			return size
		}).(pulumi.StringOutput)
	}

	return args
}

func (reg *Registry) check(args *RegistryArgs) (merr error) {
	wg := sync.WaitGroup{}
	checks := 1 // number of checks
	wg.Add(checks)
	cerr := make(chan error, checks)

	args.Namespace.ToStringOutput().ApplyT(func(ns string) (err error) {
		defer wg.Done()

		if ns == "" {
			err = errors.New("namespace could not be empty")
		}
		cerr <- err
		return
	})

	wg.Wait()
	close(cerr)

	for err := range cerr {
		merr = multierr.Append(merr, err)
	}
	return merr
}

func (reg *Registry) provision(ctx *pulumi.Context, args *RegistryArgs, opts ...pulumi.ResourceOption) (err error) {
	reg.pvc, err = corev1.NewPersistentVolumeClaim(ctx, "oci-layouts", &corev1.PersistentVolumeClaimArgs{
		Metadata: metav1.ObjectMetaArgs{
			Namespace: args.Namespace,
			Labels: pulumi.StringMap{
				"app.kubernetes.io/component": pulumi.String("fullchain"),
				"app.kubernetes.io/part-of":   pulumi.String("fullchain"),
				"ctfer.io/stack-name":         pulumi.String(ctx.Stack()),
			},
		},
		Spec: corev1.PersistentVolumeClaimSpecArgs{
			AccessModes: pulumi.ToStringArray([]string{
				"ReadWriteOnce",
			}),
			Resources: corev1.VolumeResourceRequirementsArgs{
				Requests: pulumi.StringMap{
					"storage": args.pvcStorageSize,
				},
			},
		},
	}, opts...)
	if err != nil {
		return
	}

	reg.dep, err = appsv1.NewDeployment(ctx, "registry", &appsv1.DeploymentArgs{
		Metadata: metav1.ObjectMetaArgs{
			Namespace: args.Namespace,
			Labels: pulumi.StringMap{
				"app.kubernetes.io/name":      pulumi.String("registry"),
				"app.kubernetes.io/component": pulumi.String("fullchain"),
				"app.kubernetes.io/part-of":   pulumi.String("fullchain"),
				"ctfer.io/stack-name":         pulumi.String(ctx.Stack()),
			},
		},
		Spec: appsv1.DeploymentSpecArgs{
			Selector: metav1.LabelSelectorArgs{
				MatchLabels: pulumi.StringMap{
					"app.kubernetes.io/name":      pulumi.String("registry"),
					"app.kubernetes.io/component": pulumi.String("fullchain"),
					"app.kubernetes.io/part-of":   pulumi.String("fullchain"),
					"ctfer.io/stack-name":         pulumi.String(ctx.Stack()),
				},
			},
			Template: corev1.PodTemplateSpecArgs{
				Metadata: metav1.ObjectMetaArgs{
					Namespace: args.Namespace,
					Labels: pulumi.StringMap{
						"app.kubernetes.io/name":      pulumi.String("registry"),
						"app.kubernetes.io/component": pulumi.String("fullchain"),
						"app.kubernetes.io/part-of":   pulumi.String("fullchain"),
						"ctfer.io/stack-name":         pulumi.String(ctx.Stack()),
					},
				},
				Spec: corev1.PodSpecArgs{
					Containers: corev1.ContainerArray{
						corev1.ContainerArgs{
							Name:  pulumi.String("registry"),
							Image: pulumi.Sprintf("%slibrary/registry:3", args.registry),
							Ports: corev1.ContainerPortArray{
								corev1.ContainerPortArgs{
									Name:          pulumi.String("api"),
									ContainerPort: pulumi.Int(5000),
								},
							},
							Env: func() corev1.EnvVarArrayOutput {
								envs := corev1.EnvVarArray{}
								if args.Otel != nil {
									envs = append(envs,
										corev1.EnvVarArgs{
											Name: pulumi.String("OTEL_SERVICE_NAME"),
											Value: func() pulumi.StringOutput {
												if args.Otel.ServiceName == nil {
													return pulumi.String("registry").ToStringOutput()
												}
												return args.Otel.ServiceName.ToStringPtrOutput().ApplyT(func(sn *string) string {
													if sn == nil || *sn == "" {
														return "registry"
													}
													return *sn + "-registry"
												}).(pulumi.StringOutput)
											}(),
										},
										corev1.EnvVarArgs{
											Name:  pulumi.String("OTEL_EXPORTER_OTLP_ENDPOINT"),
											Value: pulumi.Sprintf("dns://%s", args.Otel.Endpoint),
										},
										corev1.EnvVarArgs{
											Name:  pulumi.String("OTEL_EXPORTER_OTLP_PROTOCOL"),
											Value: pulumi.String("grpc"),
										},
									)
									if args.Otel.Insecure {
										envs = append(envs,
											corev1.EnvVarArgs{
												Name:  pulumi.String("OTEL_EXPORTER_OTLP_INSECURE"),
												Value: pulumi.String("true"),
											},
										)
									}
								}
								return envs.ToEnvVarArrayOutput()
							}(),
							VolumeMounts: corev1.VolumeMountArray{
								corev1.VolumeMountArgs{
									Name:      pulumi.String("oci-layouts"),
									MountPath: pulumi.String("/var/lib/registry/"),
								},
							},
						},
					},
					Volumes: corev1.VolumeArray{
						corev1.VolumeArgs{
							Name: pulumi.String("oci-layouts"),
							PersistentVolumeClaim: corev1.PersistentVolumeClaimVolumeSourceArgs{
								ClaimName: reg.pvc.Metadata.Name().Elem(),
							},
						},
					},
				},
			},
		},
	}, opts...)
	if err != nil {
		return
	}

	reg.svc, err = corev1.NewService(ctx, "registry", &corev1.ServiceArgs{
		Metadata: metav1.ObjectMetaArgs{
			Namespace: args.Namespace,
			Name:      pulumi.String("registry"),
			Labels: pulumi.StringMap{
				"app.kubernetes.io/component": pulumi.String("fullchain"),
				"app.kubernetes.io/part-of":   pulumi.String("fullchain"),
				"ctfer.io/stack-name":         pulumi.String(ctx.Stack()),
			},
		},
		Spec: corev1.ServiceSpecArgs{
			Type:      pulumi.String("NodePort"),
			ClusterIP: args.ClusterIP,
			Ports: corev1.ServicePortArray{
				corev1.ServicePortArgs{
					Name: pulumi.String("api"),
					Port: pulumi.Int(5000),
				},
			},
			Selector: reg.dep.Spec.Template().Metadata().Labels(),
		},
	}, opts...)
	if err != nil {
		return
	}

	reg.exposedNtp, err = netwv1.NewNetworkPolicy(ctx, "exposed-registry", &netwv1.NetworkPolicyArgs{
		Metadata: metav1.ObjectMetaArgs{
			Namespace: args.Namespace,
			Labels: pulumi.StringMap{
				"app.kubernetes.io/components": pulumi.String("fullchain"),
				"app.kubernetes.io/part-of":    pulumi.String("fullchain"),
				"ctfer.io/stack-name":          pulumi.String(ctx.Stack()),
			},
		},
		Spec: netwv1.NetworkPolicySpecArgs{
			PodSelector: metav1.LabelSelectorArgs{
				MatchLabels: reg.dep.Spec.Template().Metadata().Labels(),
			},
			PolicyTypes: pulumi.ToStringArray([]string{
				"Ingress",
			}),
			Ingress: netwv1.NetworkPolicyIngressRuleArray{
				netwv1.NetworkPolicyIngressRuleArgs{
					From: netwv1.NetworkPolicyPeerArray{
						netwv1.NetworkPolicyPeerArgs{
							IpBlock: &netwv1.IPBlockArgs{
								Cidr: pulumi.String("0.0.0.0/0"),
							},
						},
					},
					Ports: netwv1.NetworkPolicyPortArray{
						netwv1.NetworkPolicyPortArgs{
							Port:     reg.svc.Spec.Ports().Index(pulumi.Int(0)).Port(),
							Protocol: pulumi.String("TCP"),
						},
					},
				},
			},
		},
	}, opts...)
	if err != nil {
		return err
	}

	return nil
}

func (reg *Registry) outputs(ctx *pulumi.Context) error {
	reg.PodLabels = reg.dep.Spec.Template().Metadata().Labels()
	reg.Endpoint = pulumi.Sprintf(
		"%s.%s:%d",
		reg.svc.Metadata.Name().Elem(),
		reg.svc.Metadata.Namespace().Elem(),
		reg.svc.Spec.Ports().Index(pulumi.Int(0)).Port(),
	)
	reg.NodePort = reg.svc.Spec.Ports().Index(pulumi.Int(0)).NodePort().Elem()

	return ctx.RegisterResourceOutputs(reg, pulumi.Map{
		"podLabels": reg.PodLabels,
		"endpoint":  reg.Endpoint,
	})
}
