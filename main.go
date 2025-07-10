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
		monConf := &monitoring.MonitoringArgs{
			ColdExtract: cfg.ColdExtract,
		}
		if cfg.Registry != "" {
			monConf.Registry = pulumi.String(cfg.Registry)
		}
		mon, err := monitoring.NewMonitoring(ctx, "monitoring", monConf, opts...)
		if err != nil {
			return err
		}

		// => Namespace to deploy the platform
		ns, err := corev1.NewNamespace(ctx, "ctf", &corev1.NamespaceArgs{}, opts...)
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

			Namespace: ns.Metadata.Name().Elem(),
			Otel: &common.OtelArgs{
				Endpoint:    mon.OTEL.Endpoint,
				ServiceName: pulumi.String("24hiut2025"),
				Insecure:    true, // XXX @pandatix fix this shit
			},
		}

		if cfg.Registry != "" {
			cmConf.Registry = pulumi.String(cfg.Registry)
		}
		ch, err := challmanager.NewChallManager(ctx, "chall-manager", cmConf, opts...)
		if err != nil {
			return err
		}

		// => CTFer/CTFd
		ctfdConf := &ctfer.CTFerArgs{
			Namespace:       ns.Metadata.Name().Elem(),
			Hostname:        cfg.CTFdHostname,
			CTFdImage:       pulumi.String("ctferio/ctfd:3.7.7-0.3.4"),
			CTFdCrt:         cfg.CTFdCrt,
			CTFdKey:         cfg.CTFdKey,
			CTFdStorageSize: pulumi.String("10Gi"),
			ChallManagerUrl: pulumi.Sprintf("http://%s/api/v1", ch.Endpoint),
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

		ctx.Export("namespace", ns.Metadata.Name().Elem())
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
