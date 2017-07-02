package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-chef/chef"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/version"
)

var CHEF_CLIENT_NAME = os.Getenv("CHEF_CLIENT_NAME")
var CHEF_CLIENT_KEY = os.Getenv("CHEF_CLIENT_KEY")
var CHEF_SERVER_URL = os.Getenv("CHEF_SERVER_URL")

const (
	namespace = "chef" // For Prometheus metrics.
)

type metrics map[int]*prometheus.GaugeVec

var (
	nodeLabelNames = []string{"node"}
)

func newNodeMetric(metricName string, docString string, constLabels prometheus.Labels) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace:   namespace,
			Name:        "node_" + metricName,
			Help:        docString,
			ConstLabels: constLabels,
		},
		nodeLabelNames,
	)
}

// Exporter collects chef attributes from CHEF API and exports them using
// the prometheus metrics package.
type Exporter struct {
	mutex                       sync.RWMutex
	up                          prometheus.Gauge
	totalScrapes, ParseFailures prometheus.Counter
	nodeMetrics                 map[int]*prometheus.GaugeVec
}

func NewExporter() (*Exporter, error) {
	return &Exporter{
		up: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "up",
			Help:      "Was the last scrape successful.",
		}),
		totalScrapes: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "exporter_total_scrapes",
			Help:      "Current total scrapes.",
		}),
		ParseFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "exporter_parse_failures",
			Help:      "Number of errors while fetching metrics.",
		}),
		nodeMetrics: map[int]*prometheus.GaugeVec{
			0: newNodeMetric("ohai_time", "The time at which Ohai was last run", nil),
		},
	}, nil
}

// Describe describes all the metrics ever exported by the HAProxy exporter. It
// implements prometheus.Collector.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	for _, m := range e.nodeMetrics {
		m.Describe(ch)
	}
	ch <- e.up.Desc()
	ch <- e.totalScrapes.Desc()
	ch <- e.ParseFailures.Desc()
}

// Collect fetches the stats from configured HAProxy location and delivers them
// as Prometheus metrics. It implements prometheus.Collector.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	e.mutex.Lock() // To protect metrics from concurrent collects.
	defer e.mutex.Unlock()

	e.resetMetrics()
	e.scrape()

	ch <- e.up
	ch <- e.totalScrapes
	ch <- e.ParseFailures
	e.collectMetrics(ch)
}

func (e *Exporter) resetMetrics() {
	for _, m := range e.nodeMetrics {
		m.Reset()
	}
}

func (e *Exporter) scrape() {
	e.totalScrapes.Inc()
	key, err := ioutil.ReadFile(CHEF_CLIENT_KEY)
	if err != nil {
		fmt.Println("Couldn't read chef client key", err)
	}

	// build a client
	client, err := chef.NewClient(&chef.Config{
		Name: CHEF_CLIENT_NAME,
		Key:  string(key),
		// goiardi is on port 4545 by default. chef-zero is 8889
		BaseURL: CHEF_SERVER_URL,
	})
	if err != nil {
		fmt.Println("Issue setting up chef client:", err)
	}
	log.Print("Partial Search")
	part := make(map[string]interface{})
	part["ohai_time"] = []string{"ohai_time"}
	part["name"] = []string{"name"}
	pres, err := client.Search.PartialExec("node", "*:*", part)
	if err != nil {
		log.Fatal("Error running Search.PartialExec()", err)
	}

	for _, v := range pres.Rows {
		sec_ago := float64(999999999)
		data := v.(map[string]interface{})["data"].(map[string]interface{})
		switch ohai_time := data["ohai_time"].(type) {
		case float64:
			sec_ago = float64(time.Now().Unix()) - ohai_time
		}
		e.exportAttributes(e.nodeMetrics, sec_ago, data["name"].(string))
	}
}

func (e *Exporter) collectMetrics(metrics chan<- prometheus.Metric) {
	for _, m := range e.nodeMetrics {
		m.Collect(metrics)
	}
}

func (e *Exporter) exportAttributes(metrics map[int]*prometheus.GaugeVec, value float64, labels ...string) {
	for _, metric := range metrics {
		metric.WithLabelValues(labels...).Set(value)
	}
}

func main() {
	var (
		listenAddress = flag.String("web.listen-address", ":9101", "Address to listen on for web interface and telemetry.")
		metricsPath   = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
		showVersion   = flag.Bool("version", false, "Print version information.")
	)
	flag.Parse()
	if *showVersion {
		fmt.Fprintln(os.Stdout, version.Print("chef_exporter"))
		os.Exit(0)
	}

	log.Println("Starting chef_exporter", version.Info())
	log.Println("Build context", version.BuildContext())
	exporter, err := NewExporter()
	if err != nil {
		log.Fatal(err)
	}
	prometheus.MustRegister(exporter)
	prometheus.MustRegister(version.NewCollector("chef_exporter"))

	log.Println("Listening on", *listenAddress)
	http.Handle(*metricsPath, prometheus.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
             <head><title>Chef Exporter</title></head>
             <body>
             <h1>Chef Exporter</h1>
             <p><a href='` + *metricsPath + `'>Metrics</a></p>
             </body>
             </html>`))
	})
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}
