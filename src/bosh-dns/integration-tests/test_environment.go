package integration_tests

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"bosh-dns/dns/config"
	"bosh-dns/dns/server/record"
	"bosh-dns/dns/server/records"
	gomegadns "bosh-dns/gomega-dns"

	"bosh-dns/acceptance_tests/helpers"

	"github.com/miekg/dns"
	"github.com/onsi/gomega/gexec"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

type TestEnvironment interface {
	Start() error
	Stop() error
	Restart() error
	ServerAddress() string
	Port() int
}

func NewTestEnvironment(records []record.Record, recursors []string, caching bool, recursorSelection string, excludedRecursors []string, healthEnabled bool) TestEnvironment {
	return &testEnvironment{
		records:           records,
		recursors:         recursors,
		caching:           caching,
		recursorSelection: recursorSelection,
		excludedRecursors: excludedRecursors,
		healthEnabled:     healthEnabled,
	}
}

type testEnvironment struct {
	serverAddress string
	port          int
	ctx           context.Context
	session       *gexec.Session
	stopBoshDNS   func()
	configFile    string

	records           []record.Record
	recordsFile       string
	recursors         []string
	recursorSelection string
	excludedRecursors []string
	caching           bool
	healthEnabled     bool
}

func (t *testEnvironment) writeConfig() error {
	if err := t.writeRecords(); err != nil {
		return err
	}

	t.port = 6363
	t.serverAddress = "127.0.0.1"

	jobsDir, err := ioutil.TempDir("", "bosh-dns-integration-jobs")
	if err != nil {
		return err
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	certificateDirectory := filepath.Join(wd, "assets/certificates")
	cnf := config.Config{
		RecordsFile:       t.recordsFile,
		Recursors:         t.recursors,
		Address:           t.serverAddress,
		Port:              t.port,
		JobsDir:           jobsDir,
		RecursorSelection: t.recursorSelection,
		ExcludedRecursors: t.excludedRecursors,
		UpcheckDomains:    []string{"upcheck.bosh-dns."},
		API: config.APIConfig{
			CAFile:          filepath.Join(certificateDirectory, "bosh-dns-test-ca.crt"),
			CertificateFile: filepath.Join(certificateDirectory, "bosh-dns-test-api-certificate.crt"),
			PrivateKeyFile:  filepath.Join(certificateDirectory, "bosh-dns-test-api-certificate.key"),
		},
		Cache: config.Cache{
			Enabled: t.caching,
		},
	}

	if t.healthEnabled {
		cnf.Health = config.HealthConfig{
			Enabled:         true,
			Port:            2345,
			CertificateFile: "../healthcheck/assets/test_certs/test_client.pem",
			PrivateKeyFile:  "../healthcheck/assets/test_certs/test_client.key",
			CAFile:          "../healthcheck/assets/test_certs/test_ca.pem",
		}
	}
	configJSON, err := json.Marshal(cnf)
	if err != nil {
		return err
	}
	configTempfile, err := ioutil.TempFile("", "bosh-dns")
	t.configFile = configTempfile.Name()

	if err != nil {
		return err
	}
	if _, err := configTempfile.Write(configJSON); err != nil {
		return err
	}
	if err := configTempfile.Close(); err != nil {
		return err
	}

	return nil
}

func (t *testEnvironment) ServerAddress() string {
	return t.serverAddress
}

func (t *testEnvironment) writeRecords() error {
	swap := struct {
		Keys    []string                             `json:"record_keys"`
		Infos   [][]interface{}                      `json:"record_infos"`
		Aliases map[string][]records.AliasDefinition `json:"aliases"`
	}{}

	swap.Keys = []string{"ip", "id", "instance_index", "instance_group", "deployment", "network", "domain"}

	for _, val := range t.records {
		instanceIndex, err := strconv.Atoi(val.InstanceIndex)
		if err != nil {
			return err
		}

		swap.Infos = append(swap.Infos, []interface{}{
			val.IP,
			val.ID,
			instanceIndex,
			"bosh-dns",
			"bosh-dns",
			"default",
			"bosh",
		})
	}

	swap.Aliases = map[string][]records.AliasDefinition{
		"internal.alias": []records.AliasDefinition{
			records.AliasDefinition{
				RootDomain:   "bosh",
				HealthFilter: "smart",
			},
		},
	}

	recordsJSON, err := json.Marshal(swap)
	if err != nil {
		return err
	}

	recordsTempfile, err := ioutil.TempFile("", "bosh-dns")
	t.recordsFile = recordsTempfile.Name()

	if err != nil {
		return err
	}
	if _, err := recordsTempfile.Write(recordsJSON); err != nil {
		return err
	}
	if err := recordsTempfile.Close(); err != nil {
		return err
	}

	return nil
}

func (t *testEnvironment) Start() error {
	err := t.writeConfig()
	if err != nil {
		return err
	}

	t.ctx, t.stopBoshDNS = context.WithCancel(context.Background())
	binaryLocation, err := gexec.Build("../dns")
	if err != nil {
		return err
	}

	b := exec.CommandContext(t.ctx, binaryLocation, "--config", t.configFile)
	t.session, err = gexec.Start(b, GinkgoWriter, GinkgoWriter)
	if err != nil {
		return err
	}

	Eventually(t.checkConnection, 5*time.Second, 500*time.Millisecond).Should(ConsistOf(
		gomegadns.MatchResponse(gomegadns.Response{"ip": "127.0.0.1", "ttl": 0}),
	))

	return nil
}

func (t *testEnvironment) checkConnection() []dns.RR {
	response := helpers.DigWithOptions("upcheck.bosh-dns.", t.ServerAddress(), helpers.DigOpts{
		SkipErrCheck:   true,
		SkipRcodeCheck: true,
		Port:           t.Port(),
		Timeout:        5 * time.Millisecond,
	})

	if response == nil {
		return []dns.RR{}
	}

	return response.Answer
}

func (t *testEnvironment) Port() int {
	return t.port
}

func (t *testEnvironment) Stop() error {
	t.stopBoshDNS()

	t.session.Wait()

	return nil
}

func (t *testEnvironment) Restart() error {
	if err := t.Stop(); err != nil {
		return err
	}
	if err := t.Start(); err != nil {
		return err
	}

	return nil
}