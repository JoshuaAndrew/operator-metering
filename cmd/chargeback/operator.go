package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/clock"

	"github.com/coreos-inc/kube-chargeback/pkg/chargeback"
)

var (
	defaultHiveHost         = "hive:10000"
	defaultPrestoHost       = "presto:8080"
	defaultPromHost         = "http://prometheus.tectonic-system.svc.cluster.local:9090"
	defaultPromsumInterval  = time.Minute * 5
	defaultPromsumStepSize  = time.Minute
	defaultPromsumChunkSize = time.Minute * 5
	defaultLeaseDuration    = time.Second * 60
	// cfg is the config for our operator
	cfg chargeback.Config
)

var rootCmd = &cobra.Command{
	Use:   "chargeback",
	Short: "",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "starts the Chargeback operator",
	Run:   startChargeback,
}

func AddCommands() {
	rootCmd.AddCommand(startCmd)
}

func init() {
	log.SetLevel(log.DebugLevel)
	log.SetFormatter(&log.TextFormatter{ForceColors: true})
	startCmd.Flags().AddGoFlagSet(flag.CommandLine)
	startCmd.Flags().StringVar(&cfg.Namespace, "namespace", "", " ")
	startCmd.Flags().StringVar(&cfg.HiveHost, "hive-host", defaultHiveHost, " ")
	startCmd.Flags().StringVar(&cfg.PrestoHost, "presto-host", defaultPrestoHost, " ")
	startCmd.Flags().StringVar(&cfg.PromHost, "prom-host", defaultPromHost, " ")
	startCmd.Flags().BoolVar(&cfg.DisablePromsum, "disable-promsum", false, " ")
	startCmd.Flags().BoolVar(&cfg.LogReport, "log-report", false, "logs out report results after creating a report result table")
	startCmd.Flags().BoolVar(&cfg.LogDMLQueries, "log-dml-queries", false, "logDMLQueries controls if we log data manipulation queries made via Presto (SELECT, INSERT, etc)")
	startCmd.Flags().BoolVar(&cfg.LogDDLQueries, "log-ddl-queries", false, "logDDLQueries controls if we log data definition language queries made via Hive (CREATE TABLE, DROP TABLE, etc)")
	startCmd.Flags().DurationVar(&cfg.PromsumInterval, "promsum-interval", defaultPromsumInterval, "how often we poll prometheus")
	startCmd.Flags().DurationVar(&cfg.PromsumStepSize, "promsum-step-size", defaultPromsumStepSize, "the query step size for Promethus query. This controls resolution of results")
	startCmd.Flags().DurationVar(&cfg.PromsumChunkSize, "promsum-chunk-size", defaultPromsumChunkSize, "controls how much the range query window sizeby limiting the range query to a range of time no longer than this duration")
	startCmd.Flags().DurationVar(&cfg.LeaderLeaseDuration, "lease-duration", defaultLeaseDuration, "controls how much time elapses before declaring leader")
}

func main() {
	AddCommands()
	SetFlagsFromEnv(startCmd.Flags(), "CHARGEBACK")
	rootCmd.Execute()
}

func startChargeback(cmd *cobra.Command, args []string) {
	logger := log.WithFields(log.Fields{
		"app": "chargeback-operator",
	})
	if cfg.Namespace == "" {
		namespace, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
		if err != nil {
			logger.WithError(err).Fatal("could not determine namespace")
		}
		cfg.Namespace = string(namespace)
	}

	cfg.PodName = os.Getenv("POD_NAME")

	var err error
	cfg.Hostname, err = os.Hostname()
	if err != nil {
		logger.Fatalf("unable to get hostname, err: %s", err)
	}

	signalStopCh := setupSignals()
	runChargeback(logger, cfg, signalStopCh)
}

func runChargeback(logger log.FieldLogger, cfg chargeback.Config, stopCh <-chan struct{}) {
	clock := clock.RealClock{}
	op, err := chargeback.New(logger, cfg, clock)
	if err != nil {
		logger.WithError(err).Fatal("unable to setup Chargeback operator")
	}
	if err = op.Run(stopCh); err != nil {
		logger.WithError(err).Fatal("error occurred while the Chargeback operator was running")
	}
	logger.Infof("Chargeback operator has stopped")
}

// SetFlagsFromEnv parses all registered flags in the given flagset,
// and if they are not already set it attempts to set their values from
// environment variables. Environment variables take the name of the flag but
// are UPPERCASE, and any dashes are replaced by underscores. Environment
// variables additionally are prefixed by the given string followed by
// and underscore. For example, if prefix=PREFIX: some-flag => PREFIX_SOME_FLAG
func SetFlagsFromEnv(fs *pflag.FlagSet, prefix string) (err error) {
	alreadySet := make(map[string]bool)
	fs.Visit(func(f *pflag.Flag) {
		alreadySet[f.Name] = true
	})
	fs.VisitAll(func(f *pflag.Flag) {
		if !alreadySet[f.Name] {
			key := prefix + "_" + strings.ToUpper(strings.Replace(f.Name, "-", "_", -1))
			val := os.Getenv(key)
			if val != "" {
				if serr := fs.Set(f.Name, val); serr != nil {
					err = fmt.Errorf("invalid value %q for %s: %v", val, key, serr)
				}
			}
		}
	})
	return err
}

func setupSignals() chan struct{} {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	stopCh := make(chan struct{})
	go func() {
		sig := <-sigs
		log.Infof("got signal %s, performing shutdown", sig)
		close(stopCh)
	}()
	return stopCh
}
