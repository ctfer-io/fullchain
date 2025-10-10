package main

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"

	"github.com/ctfer-io/fullchain/parts"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg, err := InitConfig(ctx)
		if err != nil {
			return err
		}

		fch, err := parts.NewFullchain(ctx, "ctf", &parts.FullchainArgs{
			ColdExtract:          cfg.ColdExtract,
			Registry:             pulumi.String(cfg.Registry),
			WithInsideRegistry:   cfg.WithInsideRegistry,
			RegistryClusterIP:    cfg.RegistryClusterIP,
			OCIUsername:          cfg.OCIUsername,
			OCIPassword:          cfg.OCIPassword,
			ChallKubeConfig:      cfg.ChallKubeConfig,
			ChallManagerReplicas: pulumi.Int(cfg.ChallManagerReplicas),
			ChallManagerEnvs:     pulumi.ToStringMap(cfg.ChallManagerEnvs),
			EtcdReplicas:         pulumi.Int(cfg.EtcdReplicas),
			CTFdReplicas:         pulumi.Int(cfg.CTFdReplicas),
			CTFdWorkers:          pulumi.Int(cfg.CTFdWorkers),
			Image:                pulumi.String(cfg.Image),
			Crt:                  cfg.Crt,
			Key:                  cfg.Key,
			Hostname:             cfg.Hostname,
			Expose:               cfg.Expose,
			StorageClassName:     pulumi.String(cfg.StorageClassName),
		})
		if err != nil {
			return err
		}

		ctx.Export("registry.nodeport", fch.RegistryNodePort)
		ctx.Export("ctfd.nodeport", fch.CTFdNodePort)
		ctx.Export("url", fch.URL)

		return nil
	})
}

type Config struct {
	ColdExtract          bool
	WithInsideRegistry   bool
	RegistryClusterIP    pulumi.StringPtrInput
	OCIUsername          pulumi.StringInput
	OCIPassword          pulumi.StringInput
	ChallKubeConfig      pulumi.StringInput
	ChallManagerReplicas int
	ChallManagerEnvs     map[string]string
	EtcdReplicas         int
	CTFdReplicas         int
	CTFdWorkers          int
	Image                string
	Crt                  pulumi.StringInput
	Key                  pulumi.StringInput
	Hostname             pulumi.StringInput
	Registry             string
	Expose               bool
	StorageClassName     string
}

func InitConfig(ctx *pulumi.Context) (*Config, error) {
	cfg := config.New(ctx, "")

	var clusterIp *string = nil
	if cip := cfg.Get("registry-clusterip"); cip != "" {
		clusterIp = &cip
	}

	config := &Config{
		ColdExtract:          cfg.GetBool("cold-extract"),
		WithInsideRegistry:   cfg.GetBool("with-inside-registry"),
		RegistryClusterIP:    pulumi.StringPtrFromPtr(clusterIp),
		OCIUsername:          cfg.GetSecret("oci-username"),
		OCIPassword:          cfg.GetSecret("oci-password"),
		ChallKubeConfig:      cfg.GetSecret("chall-kube-config"),
		ChallManagerReplicas: cfg.GetInt("chall-manager-replicas"),
		EtcdReplicas:         cfg.GetInt("etcd-replicas"),
		CTFdReplicas:         cfg.GetInt("ctfd-replicas"),
		CTFdWorkers:          cfg.GetInt("ctfd-workers"),
		Image:                cfg.Get("image"),
		Crt:                  cfg.RequireSecret("crt"),
		Key:                  cfg.RequireSecret("key"),
		Hostname:             pulumi.String(cfg.Require("hostname")),
		Registry:             cfg.Get("registry"),
		Expose:               cfg.GetBool("expose"),
		StorageClassName:     cfg.Get("storage-class-name"),
	}

	if err := cfg.TryObject("chall-manager-envs", &config.ChallManagerEnvs); err != nil {
		config.ChallManagerEnvs = map[string]string{}
	}

	return config, nil
}
