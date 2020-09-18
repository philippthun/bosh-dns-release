package handlers

import (
	"bosh-dns/dns/server/monitoring"

	"github.com/miekg/dns"
	"golang.org/x/net/context"
)

type MetricsDNSHandler struct {
	metricsReporter monitoring.MetricsReporter
	next            dns.Handler
}

func NewMetricsDNSHandler(metricsReporter monitoring.MetricsReporter) MetricsDNSHandler {
	return MetricsDNSHandler{
		metricsReporter: metricsReporter,
	}
}

func (m MetricsDNSHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	m.metricsReporter.Report(context.Background(), w, r)
}
