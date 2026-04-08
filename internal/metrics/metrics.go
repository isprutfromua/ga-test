package metrics

import "github.com/prometheus/client_golang/prometheus"

type Metrics struct {
	SubscriptionsTotal prometheus.Counter
	ConfirmationsTotal  prometheus.Counter
	UnsubscribesTotal   prometheus.Counter
	ActiveSubscriptions prometheus.Gauge
	ScanDuration        prometheus.Histogram
	ScanErrors          prometheus.Counter
	EmailsSent          prometheus.Counter
	EmailErrors         prometheus.Counter
	GitHubRequestsTotal *prometheus.CounterVec
	GitHubRateLimitHits  prometheus.Counter
	HTTPRequestDuration *prometheus.HistogramVec
}

func New() *Metrics {
	m := &Metrics{
		SubscriptionsTotal: prometheus.NewCounter(prometheus.CounterOpts{Name: "notifier_subscriptions_created_total", Help: "Subscriptions created"}),
		ConfirmationsTotal:  prometheus.NewCounter(prometheus.CounterOpts{Name: "notifier_confirmations_total", Help: "Subscriptions confirmed"}),
		UnsubscribesTotal:   prometheus.NewCounter(prometheus.CounterOpts{Name: "notifier_unsubscribes_total", Help: "Unsubscriptions"}),
		ActiveSubscriptions: prometheus.NewGauge(prometheus.GaugeOpts{Name: "notifier_active_subscriptions", Help: "Currently active subscriptions"}),
		ScanDuration:        prometheus.NewHistogram(prometheus.HistogramOpts{Name: "notifier_scan_duration_seconds", Help: "Scan cycle duration", Buckets: prometheus.DefBuckets}),
		ScanErrors:          prometheus.NewCounter(prometheus.CounterOpts{Name: "notifier_scan_errors_total", Help: "Scan errors"}),
		EmailsSent:          prometheus.NewCounter(prometheus.CounterOpts{Name: "notifier_emails_sent_total", Help: "Emails sent successfully"}),
		EmailErrors:         prometheus.NewCounter(prometheus.CounterOpts{Name: "notifier_email_errors_total", Help: "Email failures"}),
		GitHubRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "notifier_github_requests_total", Help: "GitHub request outcomes"}, []string{"status"}),
		GitHubRateLimitHits: prometheus.NewCounter(prometheus.CounterOpts{Name: "notifier_github_rate_limit_hits_total", Help: "Rate-limit responses"}),
		HTTPRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "notifier_http_request_duration_seconds", Help: "HTTP request latency", Buckets: prometheus.DefBuckets}, []string{"method", "route", "status"}),
	}
	prometheus.MustRegister(m.SubscriptionsTotal, m.ConfirmationsTotal, m.UnsubscribesTotal, m.ActiveSubscriptions, m.ScanDuration, m.ScanErrors, m.EmailsSent, m.EmailErrors, m.GitHubRequestsTotal, m.GitHubRateLimitHits, m.HTTPRequestDuration)
	return m
}
