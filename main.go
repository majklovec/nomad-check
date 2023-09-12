package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/nomad/api"
	"github.com/hashicorp/nomad/jobspec"
)

const (
	OK       = 0
	WARNING  = 1
	CRITICAL = 2
	UNKNOWN  = 3
)

type Config struct {
	Addr        string
	TLSCert     string
	TLSKey      string
	TLSCACert   string
	TLSInsecure bool
	Timeout     int
	JobFilePath string
	CheckOnly   string
}

type Icinga struct{}

func (n *Icinga) OK(format string, a ...interface{}) {
	n.reportStatus(OK, format, a...)
}

func (n *Icinga) WARNING(format string, a ...interface{}) {
	n.reportStatus(WARNING, format, a...)
}

func (n *Icinga) CRITICAL(format string, a ...interface{}) {
	n.reportStatus(CRITICAL, format, a...)
}

func (n *Icinga) UNKNOWN(format string, a ...interface{}) {
	n.reportStatus(UNKNOWN, format, a...)
}

func (n *Icinga) reportStatus(code int, format string, a ...interface{}) {
	if len(a) > 0 {
		if err, isError := a[0].(error); isError && err == nil {
			fmt.Printf("%s\n", statusToString(code))
			return
		}
	}

	message := fmt.Sprintf(format, a...)
	fmt.Printf("%s: %s\n", statusToString(code), message)
	os.Exit(code)
}

func statusToString(code int) string {
	switch code {
	case OK:
		return "OK"
	case WARNING:
		return "WARNING"
	case CRITICAL:
		return "CRITICAL"
	default:
		return "UNKNOWN"
	}
}

func main() {
	config := parseConfig()

	// Create a Nomad client
	nomad, err := createNomadClient(config)

	icinga := &Icinga{}

	if err != nil {
		icinga.CRITICAL("Error creating Nomad client: %v", err)
	}

	// Parse the job
	job, err := parseJob(config.JobFilePath)
	if err != nil {
		icinga.CRITICAL("Error parsing job specification: %v", err)
	}

	if config.CheckOnly == "" {
		jobs := nomad.Jobs()

		// Purge the job
		jobs.Deregister(*job.ID, true, nil)

		// Register the job
		_, _, err = jobs.Register(job, nil)
		if err != nil {
			icinga.CRITICAL("Error registering job: %v", err)
		}
	}

	// Check the job status
	if config.CheckOnly == "" {
		checkJobStatus(nomad, *job.ID, config.Timeout, icinga)

	} else {
		checkJobStatus(nomad, config.CheckOnly, config.Timeout, icinga)
	}

	icinga.UNKNOWN("Unsupported status message")
}

func createNomadClient(config Config) (*api.Client, error) {
	cfg := api.DefaultConfig()
	cfg.Address = config.Addr
	if config.TLSCert != "" && config.TLSKey != "" {
		cfg.TLSConfig = &api.TLSConfig{
			CACert:     config.TLSCACert,
			ClientCert: config.TLSCert,
			ClientKey:  config.TLSKey,
			Insecure:   config.TLSInsecure,
		}
	}

	return api.NewClient(cfg)
}

func checkJobStatus(client *api.Client, jobID string, timeout int, icinga *Icinga) {
	startTime := time.Now()
	allocations := client.Allocations()

	for {
		if time.Since(startTime).Seconds() > float64(timeout) {
			icinga.CRITICAL("Job timed out")
		}

		opts := &api.QueryOptions{Params: map[string]string{"resources": "true"}}
		allocationList, _, err := allocations.List(opts)
		if err != nil {
			icinga.CRITICAL("Error getting allocations: %v", err)
		}

		for _, alloc := range allocationList {
			if alloc.JobID == jobID {
				switch alloc.ClientStatus {
				case "complete":
					icinga.WARNING("Job %s is completed", alloc.JobID)
				case "running":
					icinga.OK("Job %s running successfully", alloc.JobID)
				case "failed":
					icinga.CRITICAL("%s Job failed", alloc.JobID)
				}
			}
		}

		time.Sleep(1 * time.Second)
	}
}

func parseJob(path string) (*api.Job, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return jobspec.Parse(f)
}

func parseConfig() Config {
	var config Config

	fs := flag.NewFlagSet("Nomad Job Test", flag.ExitOnError)

	fs.StringVar(&config.Addr, "addr", "http://127.0.0.1:4646", "The address of the Nomad server")
	fs.StringVar(&config.TLSCert, "tls-cert", "", "TLS certificate to use when connecting to Nomad")
	fs.StringVar(&config.TLSKey, "tls-key", "", "TLS key to use when connecting to Nomad")
	fs.StringVar(&config.TLSCACert, "tls-ca-cert", "", "TLS CA cert to use to validate the Nomad server certificate")
	fs.BoolVar(&config.TLSInsecure, "tls-insecure", false, "Whether or not to validate the server certificate")
	fs.IntVar(&config.Timeout, "timeout", 10, "Timeout for the test job")
	fs.StringVar(&config.JobFilePath, "file", "test.nomad", "Path to the Nomad job HCL file")
	fs.StringVar(&config.CheckOnly, "check", "", "Only checks if the job ID is running")

	fs.Parse(os.Args[1:])

	return config
}
