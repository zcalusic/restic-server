package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"runtime/pprof"

	"github.com/PowerDNS/go-tlsconfig"
	"github.com/c2h5oh/datasize"
	restserver "github.com/restic/rest-server"
	"github.com/restic/rest-server/config"
	"github.com/spf13/cobra"
)

// cmdRoot is the base command when no other command has been specified.
var cmdRoot = &cobra.Command{
	Use:           "rest-server",
	Short:         "Run a REST server for use with restic",
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE:          runRoot,
	// Use this instead of other --version code when the Cobra dependency can be updated.
	//Version:       fmt.Sprintf("rest-server %s compiled with %v on %v/%v\n", version, runtime.Version(), runtime.GOOS, runtime.GOARCH),
}

var (
	showVersion  bool
	cpuProfile   string
	maxSizeBytes uint64
	tlsEnabled   bool
	configFile   string
	flagConfig   = config.Config{}
)

func init() {
	flags := cmdRoot.Flags()
	flags.StringVarP(&configFile, "config", "c", configFile, "path to YAML config file")
	flags.StringVar(&cpuProfile, "cpu-profile", cpuProfile, "write CPU profile to file")
	flags.BoolVar(&flagConfig.Debug, "debug", flagConfig.Debug, "output debug messages")
	flags.StringVar(&flagConfig.Listen, "listen", flagConfig.Listen, "listen address")
	flags.StringVar(&flagConfig.AccessLog, "log", flagConfig.AccessLog, "log HTTP requests in the combined log format")
	flags.Uint64Var(&maxSizeBytes, "max-size", uint64(flagConfig.Quota.MaxSize), "the maximum size of the repository in bytes")
	flags.StringVar(&flagConfig.Path, "path", flagConfig.Path, "data directory")
	flags.BoolVar(&tlsEnabled, "tls", flagConfig.TLS.HasCertWithKey(), "turn on TLS support")
	flags.StringVar(&flagConfig.TLS.CertFile, "tls-cert", flagConfig.TLS.CertFile, "TLS certificate path")
	flags.StringVar(&flagConfig.TLS.KeyFile, "tls-key", flagConfig.TLS.KeyFile, "TLS key path")
	flags.BoolVar(&flagConfig.Auth.Disabled, "no-auth", flagConfig.Auth.Disabled, "disable .htpasswd authentication")
	flags.BoolVar(&flagConfig.AppendOnly, "append-only", flagConfig.AppendOnly, "enable append only mode")
	flags.BoolVar(&flagConfig.PrivateRepos, "private-repos", flagConfig.PrivateRepos, "users can only access their private repo")
	flags.BoolVar(&flagConfig.Metrics.Enabled, "prometheus", flagConfig.Metrics.Enabled, "enable Prometheus metrics")
	flags.BoolVar(&flagConfig.Metrics.NoAuth, "prometheus-no-auth", flagConfig.Metrics.NoAuth, "disable auth for Prometheus /metrics endpoint")
	flags.BoolVarP(&showVersion, "version", "V", showVersion, "output version and exit")
}

var version = "0.10.0-dev"

func runRoot(cmd *cobra.Command, args []string) error {
	if showVersion {
		fmt.Printf("rest-server %s compiled with %v on %v/%v\n", version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		os.Exit(0)
	}

	log.SetFlags(0)

	// Load config
	conf := config.Default()
	if configFile != "" {
		if err := conf.LoadYAMLFile(configFile); err != nil {
			return err
		}
	}

	// Merge flag config
	conf.Quota.MaxSize = datasize.ByteSize(maxSizeBytes)
	conf.MergeFlags(flagConfig)
	if conf.Debug {
		log.Printf("Effective config:\n%s", conf.String())
	}
	if err := conf.Check(); err != nil {
		return err
	}
	if tlsEnabled && !conf.TLS.HasCertWithKey() {
		return fmt.Errorf("--tls set, but key and cert not configured")
	}

	if cpuProfile != "" {
		f, err := os.Create(cpuProfile)
		if err != nil {
			return err
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			return err
		}
		log.Println("CPU profiling enabled")
		defer pprof.StopCPUProfile()
	}

	log.Printf("Data directory: %s", conf.Path)
	if conf.Auth.Disabled {
		log.Println("Authentication disabled")
	} else {
		log.Println("Authentication enabled")
	}
	if conf.PrivateRepos {
		log.Println("Private repositories enabled")
	} else {
		log.Println("Private repositories disabled")
	}

	server, err := restserver.NewServer(*conf)
	if err != nil {
		return err
	}
	handler, err := restserver.NewHandler(server)
	if err != nil {
		return err
	}

	ctx := context.Background()
	if !conf.TLS.HasCertWithKey() {
		log.Printf("Starting server on %s\n", conf.Listen)
		return http.ListenAndServe(conf.Listen, handler)
	} else {
		log.Println("TLS enabled")
		log.Printf("Starting server on %s\n", conf.Listen)
		manager, err := tlsconfig.NewManager(ctx, conf.TLS, tlsconfig.Options{
			IsServer: true,
		})
		if err != nil {
			return err
		}
		tlsConfig, err := manager.TLSConfig()
		if err != nil {
			return err
		}
		hs := http.Server{
			Addr:      conf.Listen,
			Handler:   handler,
			TLSConfig: tlsConfig,
		}
		return hs.ListenAndServeTLS("", "") // Certificates are handled by TLSConfig
	}
}

func main() {
	if err := cmdRoot.Execute(); err != nil {
		log.Fatalf("error: %v", err)
	}
}
