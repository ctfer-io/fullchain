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
			ColdExtract:        cfg.ColdExtract,
			Registry:           pulumi.String(cfg.Registry),
			WithInsideRegistry: cfg.WithInsideRegistry,
			RegistryClusterIP:  cfg.RegistryClusterIP,
			OCIUsername:        cfg.OCIUsername,
			OCIPassword:        cfg.OCIPassword,
			ChallKubeConfig:    cfg.ChallKubeConfig,
			Crt:                cfg.Crt,
			Key:                cfg.Key,
			Hostname:           cfg.Hostname,
			Expose:             cfg.Expose,
			StorageClassName:   pulumi.String(cfg.StorageClassName),
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
	ColdExtract        bool
	WithInsideRegistry bool
	RegistryClusterIP  pulumi.StringPtrInput
	OCIUsername        pulumi.StringInput
	OCIPassword        pulumi.StringInput
	ChallKubeConfig    pulumi.StringInput
	Crt                pulumi.StringInput
	Key                pulumi.StringInput
	Hostname           pulumi.StringInput
	Registry           string
	Expose             bool
	StorageClassName   string
}

func InitConfig(ctx *pulumi.Context) (*Config, error) {
	cfg := config.New(ctx, "")

	var clusterIp *string = nil
	if cip := cfg.Get("registry-clusterip"); cip != "" {
		clusterIp = &cip
	}

	return &Config{
		ColdExtract:        cfg.GetBool("cold-extract"),
		WithInsideRegistry: cfg.GetBool("with-inside-registry"),
		RegistryClusterIP:  pulumi.StringPtrFromPtr(clusterIp),
		OCIUsername:        cfg.GetSecret("oci-username"),
		OCIPassword:        cfg.GetSecret("oci-password"),
		Crt:                cfg.RequireSecret("crt"),
		Key:                cfg.RequireSecret("key"),
		Hostname:           pulumi.String(cfg.Require("hostname")),
		Registry:           cfg.Get("registry"),
		Expose:             cfg.GetBool("expose"),
		StorageClassName:   cfg.Get("storage-class-name"),
	}, nil
}
