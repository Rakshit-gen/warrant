package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// metrics is a small Prometheus text exposition: counters, and latency
// histograms with fixed buckets. Enough for a dashboard and an alert
// without pulling in the client library.
type metrics struct {
	mu       sync.Mutex
	counters map[string]float64
	hists    map[string]*hist
}

var buckets = []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

type hist struct {
	counts []uint64
	sum    float64
	n      uint64
}

func newMetrics() *metrics {
	return &metrics{counters: map[string]float64{}, hists: map[string]*hist{}}
}

func (m *metrics) inc(name string) { m.add(name, 1) }

func (m *metrics) add(name string, v float64) {
	m.mu.Lock()
	m.counters[name] += v
	m.mu.Unlock()
}

func (m *metrics) observe(name string, v float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.hists[name]
	if h == nil {
		h = &hist{counts: make([]uint64, len(buckets))}
		m.hists[name] = h
	}
	for i, b := range buckets {
		if v <= b {
			h.counts[i]++
		}
	}
	h.sum += v
	h.n++
}

// wrap times every request by route pattern.
func (m *metrics) wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := time.Now()
		h.ServeHTTP(w, r)
		if r.Pattern != "" && !strings.HasSuffix(r.Pattern, "/events") {
			m.observe(fmt.Sprintf("warrant_http_seconds{route=%q}", r.Pattern), time.Since(t).Seconds())
		}
	})
}

func (m *metrics) handle(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	names := make([]string, 0, len(m.counters))
	for n := range m.counters {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(w, "%s %g\n", n, m.counters[n])
	}
	names = names[:0]
	for n := range m.hists {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		h := m.hists[n]
		base, labels := n, ""
		if i := strings.IndexByte(n, '{'); i >= 0 {
			base, labels = n[:i], n[i+1:len(n)-1]+","
		}
		for i, b := range buckets {
			fmt.Fprintf(w, "%s_bucket{%sle=\"%g\"} %d\n", base, labels, b, h.counts[i])
		}
		fmt.Fprintf(w, "%s_bucket{%sle=\"+Inf\"} %d\n", base, labels, h.n)
		lb := strings.TrimSuffix(labels, ",")
		if lb != "" {
			lb = "{" + lb + "}"
		}
		fmt.Fprintf(w, "%s_sum%s %g\n%s_count%s %d\n", base, lb, h.sum, base, lb, h.n)
	}
}
