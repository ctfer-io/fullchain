package parts

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/ctfer-io/chall-manager/deploy/common"
	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	netwv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/networking/v1"
	"github.com/pulumi/pulumi-random/sdk/v4/go/random"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"go.uber.org/multierr"
	"golang.org/x/crypto/bcrypt"
)

const (
	defaultPVCStorageSize = "2Gi"

	bcryptCount = 12
)

type (
	// OCI is an un-authenticated OCI registry with OpenTelemetry support.
	// It could be used to store Chall-Manager scenarios within the cluster itself
	// rather than depending on an external service.
	// It could be exposed for maintenance purposes, but should not be used as-it
	// for production purposes.
	// For production-ready deployment, please look at https://distribution.github.io/distribution/about/deploying/
	OCI struct {
		pulumi.ResourceState

		pvc        *corev1.PersistentVolumeClaim
		httpRand   *random.RandomString
		httpSec    *corev1.Secret
		authSec    *corev1.Secret
		dep        *appsv1.Deployment
		svc        *corev1.Service
		exposedNtp *netwv1.NetworkPolicy

		PodLabels pulumi.StringMapOutput
		Endpoint  pulumi.StringOutput
		NodePort  pulumi.IntOutput
	}

	OCIArgs struct {
		// Registry define from where to fetch the Docker image.
		// If set empty, defaults to Docker Hub.
		// Authentication is not supported, please provide it as Kubernetes-level configuration.
		Registry pulumi.StringPtrInput
		registry pulumi.StringOutput

		// ClusterIP is an optional argument to hardcode the service cluster IP.
		// It is mostly used to bootstrap local/demo infrastructures on a single node.
		ClusterIP pulumi.StringPtrInput

		// Namespace in which to deploy the OCI registry.
		Namespace pulumi.StringInput

		// PVCStorageSize enable to configure the storage size of the PVC the OCI registry
		// will write into (e.g. Chall-Manager scenarios).
		// Default to 2Gi.
		// See https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#meaning-of-memory
		// for syntax.
		PVCStorageSize pulumi.StringInput
		pvcStorageSize pulumi.StringOutput

		Username       pulumi.StringInput
		Password       pulumi.StringInput
		authentication bool

		OTel *common.OtelArgs
	}
)

func NewOCI(ctx *pulumi.Context, name string, args *OCIArgs, opts ...pulumi.ResourceOption) (*OCI, error) {
	reg := &OCI{}

	args = reg.defaults(args)
	if err := reg.check(args); err != nil {
		return nil, err
	}
	if err := ctx.RegisterComponentResource("ctfer-io:fullchain:oci", name, reg, opts...); err != nil {
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

func (oci *OCI) defaults(args *OCIArgs) *OCIArgs {
	if args == nil {
		args = &OCIArgs{}
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

	if args.Username != nil && args.Password != nil {
		wg := &sync.WaitGroup{}
		wg.Add(1)
		pulumi.All(args.Username, args.Password).ApplyT(func(all []any) error {
			args.authentication = all[0].(string) != "" && all[1].(string) != ""
			wg.Done()
			return nil
		})
		wg.Wait()
	}

	return args
}

func (oci *OCI) check(args *OCIArgs) (merr error) {
	wg := sync.WaitGroup{}
	checks := 1 // number of checks
	if args.authentication {
		checks++
	}
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
	if args.authentication {
		args.Password.ToStringOutput().ApplyT(func(password string) (err error) {
			defer wg.Done()

			_, err = bcrypt.GenerateFromPassword([]byte(password), bcryptCount)
			cerr <- err
			return
		})
	}

	wg.Wait()
	close(cerr)

	for err := range cerr {
		merr = multierr.Append(merr, err)
	}
	return merr
}

func (oci *OCI) provision(ctx *pulumi.Context, args *OCIArgs, opts ...pulumi.ResourceOption) (err error) {
	oci.pvc, err = corev1.NewPersistentVolumeClaim(ctx, "oci-layouts", &corev1.PersistentVolumeClaimArgs{
		Metadata: metav1.ObjectMetaArgs{
			Namespace: args.Namespace,
			Labels: pulumi.StringMap{
				"app.kubernetes.io/component": pulumi.String("oci"),
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

	oci.httpRand, err = random.NewRandomString(ctx, "random-http-secret", &random.RandomStringArgs{
		Length:  pulumi.Int(16),
		Special: pulumi.Bool(false),
	}, opts...)
	if err != nil {
		return
	}

	oci.httpSec, err = corev1.NewSecret(ctx, "http-secret", &corev1.SecretArgs{
		Metadata: metav1.ObjectMetaArgs{
			Namespace: args.Namespace,
			Labels: pulumi.StringMap{
				"app.kubernetes.io/component": pulumi.String("oci"),
				"app.kubernetes.io/part-of":   pulumi.String("fullchain"),
				"ctfer.io/stack-name":         pulumi.String(ctx.Stack()),
			},
		},
		StringData: pulumi.StringMap{
			"http-secret": oci.httpRand.Result,
		},
		Immutable: pulumi.Bool(true),
	}, opts...)
	if err != nil {
		return
	}

	if args.authentication {
		oci.authSec, err = corev1.NewSecret(ctx, "htpasswd", &corev1.SecretArgs{
			Metadata: metav1.ObjectMetaArgs{
				Namespace: args.Namespace,
				Labels: pulumi.StringMap{
					"app.kubernetes.io/component": pulumi.String("oci"),
					"app.kubernetes.io/part-of":   pulumi.String("fullchain"),
					"ctfer.io/stack-name":         pulumi.String(ctx.Stack()),
				},
			},
			StringData: pulumi.StringMap{
				"htpasswd": pulumi.All(args.Username, args.Password).ApplyT(func(all []any) string {
					username := all[0].(string)
					password := all[1].(string)

					hashed, _ := bcrypt.GenerateFromPassword([]byte(password), bcryptCount)
					return fmt.Sprintf("%s:%s", username, hashed)
				}).(pulumi.StringOutput),
			},
			Immutable: pulumi.Bool(true),
		}, opts...)
		if err != nil {
			return
		}
	}

	oci.dep, err = appsv1.NewDeployment(ctx, "oci", &appsv1.DeploymentArgs{
		Metadata: metav1.ObjectMetaArgs{
			Namespace: args.Namespace,
			Labels: pulumi.StringMap{
				"app.kubernetes.io/name":      pulumi.String("oci"),
				"app.kubernetes.io/version":   pulumi.String("3"),
				"app.kubernetes.io/component": pulumi.String("oci"),
				"app.kubernetes.io/part-of":   pulumi.String("fullchain"),
				"ctfer.io/stack-name":         pulumi.String(ctx.Stack()),
			},
		},
		Spec: appsv1.DeploymentSpecArgs{
			Selector: metav1.LabelSelectorArgs{
				MatchLabels: pulumi.StringMap{
					"app.kubernetes.io/name":      pulumi.String("oci"),
					"app.kubernetes.io/version":   pulumi.String("3"),
					"app.kubernetes.io/component": pulumi.String("oci"),
					"app.kubernetes.io/part-of":   pulumi.String("fullchain"),
					"ctfer.io/stack-name":         pulumi.String(ctx.Stack()),
				},
			},
			Template: corev1.PodTemplateSpecArgs{
				Metadata: metav1.ObjectMetaArgs{
					Namespace: args.Namespace,
					Labels: pulumi.StringMap{
						"app.kubernetes.io/name":      pulumi.String("oci"),
						"app.kubernetes.io/version":   pulumi.String("3"),
						"app.kubernetes.io/component": pulumi.String("oci"),
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
								envs := corev1.EnvVarArray{
									corev1.EnvVarArgs{
										Name: pulumi.String("REGISTRY_HTTP_SECRET"),
										ValueFrom: corev1.EnvVarSourceArgs{
											SecretKeyRef: corev1.SecretKeySelectorArgs{
												Name: oci.httpSec.Metadata.Name().Elem(),
												Key:  pulumi.String("http-secret"),
											},
										},
									},
								}
								if args.Username != nil && args.Password != nil {
									envs = append(envs,
										corev1.EnvVarArgs{
											Name:  pulumi.String("REGISTRY_AUTH_HTPASSWD_REALM"),
											Value: pulumi.String("/auth/htpasswd"),
										},
										corev1.EnvVarArgs{
											Name:  pulumi.String("REGISTRY_AUTH_HTPASSWD_PATH"),
											Value: pulumi.String("/.htpasswd"),
										},
									)
								}
								if args.OTel != nil {
									envs = append(envs,
										corev1.EnvVarArgs{
											Name: pulumi.String("OTEL_SERVICE_NAME"),
											Value: func() pulumi.StringOutput {
												if args.OTel.ServiceName == nil {
													return pulumi.String("oci").ToStringOutput()
												}
												return args.OTel.ServiceName.ToStringPtrOutput().ApplyT(func(sn *string) string {
													if sn == nil || *sn == "" {
														return "oci"
													}
													return *sn + "-oci"
												}).(pulumi.StringOutput)
											}(),
										},
										corev1.EnvVarArgs{
											Name:  pulumi.String("OTEL_EXPORTER_OTLP_ENDPOINT"),
											Value: pulumi.Sprintf("dns://%s", args.OTel.Endpoint),
										},
										corev1.EnvVarArgs{
											Name:  pulumi.String("OTEL_EXPORTER_OTLP_PROTOCOL"),
											Value: pulumi.String("grpc"),
										},
									)
									if args.OTel.Insecure {
										envs = append(envs,
											corev1.EnvVarArgs{
												Name:  pulumi.String("OTEL_EXPORTER_OTLP_INSECURE"),
												Value: pulumi.String("true"),
											},
										)
									}
								} else {
									envs = append(envs, corev1.EnvVarArgs{
										Name:  pulumi.String("OTEL_TRACES_EXPORTER"),
										Value: pulumi.String("none"),
									})
								}
								return envs.ToEnvVarArrayOutput()
							}(),
							VolumeMounts: func() corev1.VolumeMountArray {
								arr := []corev1.VolumeMountInput{
									corev1.VolumeMountArgs{
										Name:      pulumi.String("oci-layouts"),
										MountPath: pulumi.String("/var/lib/registry/"),
									},
								}
								if args.authentication {
									arr = append(arr, corev1.VolumeMountArgs{
										Name:      pulumi.String("htpasswd"),
										MountPath: pulumi.String("/"),
										ReadOnly:  pulumi.Bool(true),
									})
								}
								return arr
							}(),
						},
					},
					Volumes: func() corev1.VolumeArray {
						arr := []corev1.VolumeInput{
							corev1.VolumeArgs{
								Name: pulumi.String("oci-layouts"),
								PersistentVolumeClaim: corev1.PersistentVolumeClaimVolumeSourceArgs{
									ClaimName: oci.pvc.Metadata.Name().Elem(),
								},
							},
						}
						if args.authentication {
							arr = append(arr, corev1.VolumeArgs{
								Name: pulumi.String("htpasswd"),
								Secret: corev1.SecretVolumeSourceArgs{
									SecretName:  oci.authSec.Metadata.Name().Elem(),
									DefaultMode: pulumi.Int(0644),
									Items: corev1.KeyToPathArray{
										corev1.KeyToPathArgs{
											Key:  pulumi.String("htpasswd"),
											Path: pulumi.String(".htpasswd"),
										},
									},
								},
							})
						}
						return arr
					}(),
				},
			},
		},
	}, opts...)
	if err != nil {
		return
	}

	oci.svc, err = corev1.NewService(ctx, "oci", &corev1.ServiceArgs{
		Metadata: metav1.ObjectMetaArgs{
			Namespace: args.Namespace,
			Name:      pulumi.String("oci"),
			Labels: pulumi.StringMap{
				"app.kubernetes.io/component": pulumi.String("oci"),
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
			Selector: oci.dep.Spec.Template().Metadata().Labels(),
		},
	}, opts...)
	if err != nil {
		return
	}

	oci.exposedNtp, err = netwv1.NewNetworkPolicy(ctx, "exposed-oci", &netwv1.NetworkPolicyArgs{
		Metadata: metav1.ObjectMetaArgs{
			Namespace: args.Namespace,
			Labels: pulumi.StringMap{
				"app.kubernetes.io/component": pulumi.String("oci"),
				"app.kubernetes.io/part-of":   pulumi.String("fullchain"),
				"ctfer.io/stack-name":         pulumi.String(ctx.Stack()),
			},
		},
		Spec: netwv1.NetworkPolicySpecArgs{
			PodSelector: metav1.LabelSelectorArgs{
				MatchLabels: oci.dep.Spec.Template().Metadata().Labels(),
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
							Port:     oci.svc.Spec.Ports().Index(pulumi.Int(0)).Port(),
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

func (oci *OCI) outputs(ctx *pulumi.Context) error {
	oci.PodLabels = oci.dep.Spec.Template().Metadata().Labels()
	oci.Endpoint = pulumi.Sprintf(
		"%s.%s:%d",
		oci.svc.Metadata.Name().Elem(),
		oci.svc.Metadata.Namespace().Elem(),
		oci.svc.Spec.Ports().Index(pulumi.Int(0)).Port(),
	)
	oci.NodePort = oci.svc.Spec.Ports().Index(pulumi.Int(0)).NodePort().Elem()

	return ctx.RegisterResourceOutputs(oci, pulumi.Map{
		"podLabels": oci.PodLabels,
		"endpoint":  oci.Endpoint,
		"nodePort":  oci.NodePort,
	})
}
