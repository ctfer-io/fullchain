package main

import (
	"github.com/ctfer-io/chall-manager/deploy/services/parts"
	"github.com/ctfer-io/fullchain/services"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
	"go.uber.org/multierr"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg, err := InitConfig(ctx)
		if err != nil {
			return err
		}

		fch, err := services.NewFullchain(ctx, "ctf", &services.FullchainArgs{
			Monitoring: &services.MonitoringArgs{
				StorageClassName: pulumi.String(cfg.Monitoring.StorageClassName),
				StorageSize:      pulumi.String(cfg.Monitoring.StorageSize),
				PVCAccessModes:   pulumi.ToStringArray(cfg.Monitoring.PVCAccessModes),
				ColdExtract:      cfg.Monitoring.ColdExtract,
			},
			ChallManager: &services.ChallManagerArgs{
				Tag:            pulumi.String(cfg.ChallManager.Tag),
				LogLevel:       pulumi.String(cfg.ChallManager.LogLevel),
				EtcdReplicas:   pulumi.IntPtrFromPtr(cfg.ChallManager.EtcdReplicas),
				Replicas:       pulumi.IntPtrFromPtr(cfg.ChallManager.Replicas),
				JanitorCron:    pulumi.String(cfg.ChallManager.JanitorCron),
				JanitorTicker:  pulumi.String(cfg.ChallManager.JanitorTicker),
				JanitorMode:    parts.JanitorMode(cfg.ChallManager.JanitorMode),
				PVCAccessModes: pulumi.ToStringArray(cfg.ChallManager.PVCAccessModes),
				PVCStorageSize: pulumi.String(cfg.ChallManager.PVCStorageSize),
				Kubeconfig:     pulumi.String(cfg.ChallManager.Kubeconfig),
				Requests:       pulumi.ToStringMap(cfg.ChallManager.Requests),
				Limits:         pulumi.ToStringMap(cfg.ChallManager.Limits),
				Envs:           pulumi.ToStringMap(cfg.ChallManager.Envs),
				Swagger:        cfg.ChallManager.Swagger,
				OCIInsecure:    cfg.ChallManager.OCIInsecure,
			},
			CTFer: &services.CTFerArgs{
				Platform: &services.PlatformArgs{
					Image:              pulumi.String(cfg.CTFer.Platform.Image),
					Crt:                pulumi.String(cfg.CTFer.Platform.Crt),
					Key:                pulumi.String(cfg.CTFer.Platform.Key),
					StorageSize:        pulumi.String(cfg.CTFer.Platform.StorageSize),
					Workers:            pulumi.Int(cfg.CTFer.Platform.Workers),
					Replicas:           pulumi.Int(cfg.CTFer.Platform.Replicas),
					Requests:           pulumi.ToStringMap(cfg.CTFer.Platform.Requests),
					Limits:             pulumi.ToStringMap(cfg.CTFer.Platform.Limits),
					StorageClass:       pulumi.String(cfg.CTFer.Platform.StorageClass),
					PVCAccessModes:     pulumi.ToStringArray(cfg.CTFer.Platform.PVCAccessModes),
					Hostname:           pulumi.String(cfg.CTFer.Platform.Hostname),
					IngressAnnotations: pulumi.ToStringMap(cfg.CTFer.Platform.IngressAnnotations),
				},
				DB: &services.DBArgs{
					StorageClassName:  pulumi.String(cfg.CTFer.DB.StorageClassName),
					OperatorNamespace: pulumi.String(cfg.CTFer.DB.OperatorNamespace),
					Replicas:          pulumi.Int(cfg.CTFer.DB.Replicas),
				},
				Cache: &services.CacheArgs{
					Replicas: pulumi.Int(cfg.CTFer.Cache.Replicas),
				},
				Expose: cfg.CTFer.Expose,
			},
			OCI: &services.OCIArgs{
				Username:           pulumi.String(cfg.OCI.Username),
				Password:           pulumi.String(cfg.OCI.Password),
				WithInsideRegistry: cfg.OCI.WithInsideRegistry,
				ClusterIP:          pulumi.String(cfg.OCI.ClusterIP),
				PVCStorageSize:     pulumi.String(cfg.OCI.PVCStorageSize),
			},
			IngressNamespace: pulumi.String(cfg.IngressNamespace),
			IngressLabels:    pulumi.ToStringMap(cfg.IngressLabels),
			Registry:         pulumi.String(cfg.Registry),
		})
		if err != nil {
			return err
		}

		ctx.Export("oci.nodeport", fch.OCINodePort)
		ctx.Export("ctfd.nodeport", fch.CTFdNodePort)
		ctx.Export("url", fch.URL)

		return nil
	})
}

type Config struct {
	Monitoring   *Monitoring
	ChallManager *ChallManager
	CTFer        *CTFer
	OCI          *OCI

	IngressNamespace string
	IngressLabels    map[string]string
	Registry         string
}

type Monitoring struct {
	StorageClassName string   `json:"storage-class-name"`
	StorageSize      string   `json:"storage-size"`
	PVCAccessModes   []string `json:"pvc-access-modes"`
	ColdExtract      bool     `json:"cold-axtract"`
}

type ChallManager struct {
	Tag            string            `json:"tag"`
	LogLevel       string            `json:"logLevel"`
	EtcdReplicas   *int              `json:"etcd-replicas,omitempty"`
	Replicas       *int              `json:"replicas,omitempty"`
	JanitorCron    string            `json:"janitor-cron"`
	JanitorTicker  string            `json:"janitor-ticker"`
	JanitorMode    string            `json:"janitor-mode"`
	PVCAccessModes []string          `json:"pvc-access-modes"`
	PVCStorageSize string            `json:"pvc-storage-size"`
	Kubeconfig     string            `json:"kubeconfig"` // TODO make it a secret
	Requests       map[string]string `json:"requests"`
	Limits         map[string]string `json:"limits"`
	Envs           map[string]string `json:"envs"`
	Swagger        bool              `json:"swagger"`
	OCIInsecure    bool              `json:"oci-insecure"`
}

type CTFer struct {
	Platform *Platform `json:"platform"`
	DB       *DB       `json:"db"`
	Cache    *Cache    `json:"cache"`
	Expose   bool      `json:"expose"`
}

type Platform struct {
	Image              string            `json:"image"`
	Crt                string            `json:"crt"` // TODO make it a secret
	Key                string            `json:"key"` // TODO make it a secret
	StorageSize        string            `json:"storage-size"`
	Workers            int               `json:"workers"`
	Replicas           int               `json:"replicas"`
	Requests           map[string]string `json:"requests"`
	Limits             map[string]string `json:"limits"`
	StorageClass       string            `json:"storage-class"`
	PVCAccessModes     []string          `json:"pvc-access-modes"`
	Hostname           string            `json:"hostname"`
	IngressAnnotations map[string]string `json:"ingress-annotations"`
}

type DB struct {
	StorageClassName  string `json:"storage-class-name"`
	OperatorNamespace string `json:"operator-namespace"`
	Replicas          int    `json:"replicas"`
}

type Cache struct {
	Replicas int `json:"replicas"`
}

type OCI struct {
	Username           string `json:"username"`
	Password           string `json:"password"` // TODO make it a secret
	WithInsideRegistry bool   `json:"with-inside-registry"`
	ClusterIP          string `json:"cluster-ip"`
	PVCStorageSize     string `json:"pvc-storage-size"`
}

func InitConfig(ctx *pulumi.Context) (*Config, error) {
	cfg := config.New(ctx, "")

	conf := &Config{
		Monitoring:   &Monitoring{},
		ChallManager: &ChallManager{},
		CTFer: &CTFer{
			Platform: &Platform{},
			DB:       &DB{},
			Cache:    &Cache{},
		},
		OCI:              &OCI{},
		IngressNamespace: cfg.Get("ingress-namespace"),
		Registry:         cfg.Get("registry"),
	}

	if err := multierr.Combine(
		cfg.GetObject("monitoring", conf.Monitoring),
		cfg.GetObject("chall-manager", conf.ChallManager),
		cfg.GetObject("ctfer", conf.CTFer),
		cfg.GetObject("oci", conf.OCI),
		cfg.GetObject("ingress-labels", conf.IngressLabels),
	); err != nil {
		return nil, err
	}

	if cpu := cfg.Get("ctfer-platform-requests-cpu"); cpu != "" {
		conf.CTFer.Platform.Requests["cpu"] = cpu
	}
	if memory := cfg.Get("ctfer-platform-requests-memory"); memory != "" {
		conf.CTFer.Platform.Requests["memory"] = memory
	}

	return conf, nil
}
