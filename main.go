package main

import (
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"

	"github.com/ctfer-io/chall-manager/deploy/common"
	challmanager "github.com/ctfer-io/chall-manager/deploy/services"
	ctfer "github.com/ctfer-io/ctfer/services"
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
		mon, err := monitoring.NewMonitoring(ctx, "monitoring", &monitoring.MonitoringArgs{
			ColdExtract: cfg.ColdExtract,
		}, opts...)
		if err != nil {
			return err
		}

		// => Namespace to deploy the platform
		ns, err := corev1.NewNamespace(ctx, "ctf", &corev1.NamespaceArgs{}, opts...)
		if err != nil {
			return err
		}

		// => Chall-Manager
		ch, err := challmanager.NewChallManager(ctx, "chall-manager", &challmanager.ChallManagerArgs{
			Kubeconfig: cfg.ChallKubeConfig,
			Tag:        pulumi.String("v0.4.3"),
			Namespace:  ns.Metadata.Name().Elem(),
			Otel: &common.OtelArgs{
				Endpoint: mon.OTEL.Endpoint,
				Insecure: true, // XXX @pandatix fix this shit
			},
		}, opts...)
		if err != nil {
			return err
		}
		_ = ch

		// => CTFer/CTFd
		ctfer, err := ctfer.NewCTFer(ctx, "platform", &ctfer.CTFerArgs{
			Namespace:        ns.Metadata.Name().Elem(),
			Hostname:         pulumi.String("ctfd.24hiut2025.ctfer.io"),
			CTFdImage:        pulumi.String("ctferio/ctfd:3.7.7-0.3.2"),
			ChartsRepository: pulumi.String(""),
			ImagesRepository: pulumi.String(""),
			ChallManagerUrl:  pulumi.Sprintf("http://%s/api/v1", ch.Endpoint),
		}, opts...)
		if err != nil {
			return err
		}

		ctx.Export("url", ctfer.URL)
		return nil
	})
}

type Config struct {
	ColdExtract     bool
	ChallKubeConfig pulumi.StringInput
}

func InitConfig(ctx *pulumi.Context) (*Config, error) {
	cfg := config.New(ctx, "")
	return &Config{
		ColdExtract:     cfg.GetBool("cold-extract"),
		ChallKubeConfig: cfg.GetSecret("chall-kube-config"),
	}, nil
}
