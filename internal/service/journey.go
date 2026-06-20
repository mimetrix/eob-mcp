package service

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// journey.go — the RACE "probe journey" dashboard producer. It is the
// HUMAN face of the same probe→NATS substrate the MCP tools expose
// (see ARCHITECTURE.md): a read-only subscriber to the RACE audit bus
// that aggregates denial events into a single /journey.json contract
// (see JOURNEY_DASHBOARD.md) and serves a self-contained HTML viewer.
//
// Strictly passive: it only observes the bus. The probe stimulus that
// lights the channels (e.g. an external nmap -O) is an out-of-band
// operator action and is deliberately NOT a feature of this server.

//go:embed race-journey.html
var raceJourneyHTML []byte

const (
	probesSubject  = "defense.events.probes"
	threatsSubject = "defense.events.threats"

	journeyHistoryLen   = 120              // samples kept per channel (≈2 min @ 1s)
	journeySampleEvery  = time.Second      // chart resolution
	journeyChannelIdle  = 20 * time.Second // drop a channel's series after this much silence
	journeyProberTTL    = 90 * time.Second // forget a prober after this much silence
	journeyIndexRefresh = 30 * time.Second // rebuild the k8s resolution snapshot this often
	journeyActiveWindow = 10 * time.Second // "mode=denial" if any event seen within this
	journeyTopProbers   = 8
)

// channelNames mirrors CHANNEL_* in eob-defense-agent/kernel/maps.h.
// Keep in sync — single source of truth for the id→name mapping.
var channelNames = map[int]string{
	1: "BANNER_SSH", 2: "BANNER_HTTP", 3: "OSFP", 4: "ERRNO_FS",
	5: "PROC_KALLSYMS", 6: "CRASH", 7: "ICMP", 8: "SEQ_IPID", 9: "DECOY",
}

func channelName(id int) string {
	if n, ok := channelNames[id]; ok {
		return n
	}
	return fmt.Sprintf("ch%d", id)
}

// probeEnvelope is the slice of a defense.events.probes message we use.
// NOTE: on the *core* subject the publish is unwrapped (no JetStream
// {subject,sequence,timestamp,data} envelope — that wrapper only appears
// when reading the defense-events-ring via stream_read).
type probeEnvelope struct {
	Cluster string `json:"cluster"`
	SiteID  string `json:"site_id"`
	Event   struct {
		ChannelID  int    `json:"channel_id"`
		Decision   string `json:"decision"`
		Prober     string `json:"prober"`
		Responder  string `json:"responder"`
		Substitute string `json:"substitute"`
	} `json:"event"`
}

type proberStat struct {
	ip           string
	name         string // resolved workload/node/external; "" until first resolve
	denied       int64
	lastDecision string
	lastSeen     time.Time
}

// Journey is the read-only RACE dashboard producer. Construct with
// NewJourney; call Start to connect+subscribe, Close to drain.
type Journey struct {
	svc *Server

	mu          sync.Mutex
	counts      map[string]int       // events this sample interval, per channel
	series      map[string][]float64 // per-channel rate history (active only)
	lastSeen    map[string]time.Time // per-channel last event time
	lastSub     map[string]string    // per-channel last substitute payload (station copy)
	probers     map[string]*proberStat
	tarpit      int64
	decoyHits   int64
	threats     int64
	totalDenied int64
	lastEventAt time.Time

	idx        *endpointIndex
	idxBuiltAt time.Time

	nc   *nats.Conn
	subs []*nats.Subscription
	stop chan struct{}
	done chan struct{}
}

// NewJourney builds the producer bound to the shared service (for cfg +
// the k8s endpoint resolver). It does not connect until Start.
func NewJourney(s *Server) *Journey {
	return &Journey{
		svc:      s,
		counts:   map[string]int{},
		series:   map[string][]float64{},
		lastSeen: map[string]time.Time{},
		lastSub:  map[string]string{},
		probers:  map[string]*proberStat{},
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start connects to the defense bus and subscribes to the RACE audit
// subjects, then launches the 1 s sampler. Returns an error if the
// initial connect fails; the caller may choose to proceed without the
// dashboard.
func (j *Journey) Start() error {
	url := j.svc.cfg.DefenseNATSURL
	if url == "" {
		return fmt.Errorf("journey: EOB_DEFENSE_NATS_URL unset")
	}
	nc, err := nats.Connect(url,
		nats.Name("eob-mcp-journey"),
		nats.Timeout(5*time.Second),
		nats.MaxReconnects(-1),
		nats.RetryOnFailedConnect(true),
	)
	if err != nil {
		return fmt.Errorf("journey: connect defense NATS %s: %w", url, err)
	}
	j.nc = nc

	for _, subj := range []string{probesSubject, threatsSubject} {
		s, err := nc.Subscribe(subj, j.onMessage)
		if err != nil {
			j.closeNATS()
			return fmt.Errorf("journey: subscribe %s: %w", subj, err)
		}
		j.subs = append(j.subs, s)
	}
	go j.sampleLoop()
	slog.Info("journey producer started", "url", url,
		"subjects", []string{probesSubject, threatsSubject})
	return nil
}

// Close unsubscribes, stops the sampler, and drains the connection.
func (j *Journey) Close() {
	if j.nc == nil {
		return
	}
	close(j.stop)
	<-j.done
	j.closeNATS()
}

func (j *Journey) closeNATS() {
	for _, s := range j.subs {
		_ = s.Unsubscribe()
	}
	if j.nc != nil {
		_ = j.nc.Drain()
		j.nc = nil
	}
}

func (j *Journey) onMessage(m *nats.Msg) {
	if m.Subject == threatsSubject {
		j.mu.Lock()
		j.threats++
		j.mu.Unlock()
		return
	}
	var e probeEnvelope
	if json.Unmarshal(m.Data, &e) != nil || e.Event.ChannelID == 0 {
		return
	}
	ch := channelName(e.Event.ChannelID)
	ip := hostOf(e.Event.Prober)
	now := time.Now()

	j.mu.Lock()
	defer j.mu.Unlock()
	j.counts[ch]++
	j.lastSeen[ch] = now
	j.totalDenied++
	j.lastEventAt = now
	if e.Event.Substitute != "" {
		j.lastSub[ch] = sanitizeSub(e.Event.Substitute)
	}
	switch e.Event.Decision {
	case "Tarpit":
		j.tarpit++
	case "DecoyHit":
		j.decoyHits++
	}
	if ip != "" {
		p := j.probers[ip]
		if p == nil {
			p = &proberStat{ip: ip}
			j.probers[ip] = p
		}
		p.denied++
		p.lastDecision = e.Event.Decision
		p.lastSeen = now
	}
}

// sampleLoop snapshots per-channel rates into the history ring once per
// interval, prunes idle channels/probers, and refreshes the k8s
// resolution snapshot periodically.
func (j *Journey) sampleLoop() {
	defer close(j.done)
	t := time.NewTicker(journeySampleEvery)
	defer t.Stop()
	for {
		select {
		case <-j.stop:
			return
		case <-t.C:
			j.sample()
		}
	}
}

func (j *Journey) sample() {
	now := time.Now()
	j.maybeRefreshIndex(now)

	j.mu.Lock()
	defer j.mu.Unlock()

	// Per-channel: append this interval's count as the rate, then reset.
	// Active channels with no events this tick get a 0 (keeps the line
	// moving); channels idle past the window are dropped entirely so a
	// silent channel doesn't linger as a frozen ghost line.
	active := map[string]bool{}
	for ch, last := range j.lastSeen {
		if now.Sub(last) <= journeyChannelIdle {
			active[ch] = true
		}
	}
	for ch := range active {
		v := float64(j.counts[ch])
		s := append(j.series[ch], v)
		if len(s) > journeyHistoryLen {
			s = s[len(s)-journeyHistoryLen:]
		}
		j.series[ch] = s
	}
	for ch := range j.series {
		if !active[ch] {
			delete(j.series, ch)
			delete(j.lastSeen, ch)
			delete(j.lastSub, ch)
		}
	}
	j.counts = map[string]int{}

	// Prune stale probers, then resolve any unnamed ones against the
	// current index snapshot.
	for ip, p := range j.probers {
		if now.Sub(p.lastSeen) > journeyProberTTL {
			delete(j.probers, ip)
			continue
		}
		if p.name == "" && j.idx != nil {
			r := j.idx.resolve(ip, 0)
			p.name = describeEndpoint(r.Kind, r.Namespace, r.Name)
		}
	}
}

func (j *Journey) maybeRefreshIndex(now time.Time) {
	if j.svc.kube == nil {
		return
	}
	j.mu.Lock()
	stale := j.idx == nil || now.Sub(j.idxBuiltAt) > journeyIndexRefresh
	j.mu.Unlock()
	if !stale {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), k8sCallTimeout)
	idx := j.svc.buildEndpointIndex(ctx)
	cancel()
	j.mu.Lock()
	j.idx = idx
	j.idxBuiltAt = now
	// Re-resolve everyone against the fresh snapshot.
	for ip, p := range j.probers {
		r := idx.resolve(ip, 0)
		p.name = describeEndpoint(r.Kind, r.Namespace, r.Name)
	}
	j.mu.Unlock()
}

// ---- HTTP handlers ----------------------------------------------------

// ServeHTML serves the self-contained dashboard page.
func (j *Journey) ServeHTML(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raceJourneyHTML)
}

// ServeJSON serves the live /journey.json contract (read-only, CORS-open
// so the static page can poll it from anywhere).
func (j *Journey) ServeJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(j.snapshot())
}

func (j *Journey) snapshot() map[string]any {
	now := time.Now()
	j.mu.Lock()
	defer j.mu.Unlock()

	channels := map[string]float64{}
	seriesOut := map[string][]float64{}
	var tickTotal float64
	for ch, s := range j.series {
		seriesOut[ch] = s
		if len(s) > 0 {
			channels[ch] = s[len(s)-1]
			tickTotal += s[len(s)-1]
		}
	}

	probers := make([]map[string]any, 0, len(j.probers))
	for _, p := range j.probers {
		name := p.name
		if name == "" {
			name = "external"
		}
		probers = append(probers, map[string]any{
			"ip": p.ip, "name": name, "denied": p.denied,
			"last_decision": p.lastDecision,
		})
	}
	sort.Slice(probers, func(a, b int) bool {
		return probers[a]["denied"].(int64) > probers[b]["denied"].(int64)
	})
	if len(probers) > journeyTopProbers {
		probers = probers[:journeyTopProbers]
	}

	mode := "idle"
	if !j.lastEventAt.IsZero() && now.Sub(j.lastEventAt) <= journeyActiveWindow {
		mode = "denial"
	}

	cfg := j.svc.cfg
	headline := fmt.Sprintf("RACE %s · %d channels active · %.0f/s denied · %d total",
		mode, len(j.series), tickTotal, j.totalDenied)

	return map[string]any{
		"updated_ms": now.UnixMilli(),
		"headline":   headline,
		"hero":       fmt.Sprintf("%.0f probes/s denied", tickTotal),
		"mode":       mode,
		"channels":   channels,
		"channel_series": seriesOut,
		"site": map[string]any{
			"site_id": cfg.SiteID, "tenant": cfg.Tenant, "cluster": cfg.SiteID,
			"region": cfg.Region, "iface": "vhost0", "mode": mode,
		},
		"probers": probers,
		"alarms":  map[string]any{"tarpit": j.tarpit, "decoy_hits": j.decoyHits},
		"n_channels": len(channelNames),
		"n_active":   len(j.series),
		"n_probers":  len(j.probers),
		"stations":   j.stations(tickTotal),
	}
}

// stations synthesizes the per-station live drilldown text. Keys match
// the STATIONS ids in race-journey.html.
func (j *Journey) stations(tickTotal float64) map[string]any {
	st := func(live, status string) map[string]any {
		return map[string]any{"live": live, "status": status}
	}
	chCount := func(name string) float64 {
		if s := j.series[name]; len(s) > 0 {
			return s[len(s)-1]
		}
		return 0
	}
	decoyStatus := "ok"
	if j.decoyHits > 0 {
		decoyStatus = "hit"
	}
	tarpitStatus := "ok"
	if j.tarpit > 0 {
		tarpitStatus = "warn"
	}
	osfpLive := fmt.Sprintf("%.0f/s", chCount("OSFP"))
	if s := j.lastSub["OSFP"]; s != "" {
		osfpLive += "  last: " + s
	}
	bannerLive := fmt.Sprintf("SSH %.0f/s · HTTP %.0f/s", chCount("BANNER_SSH"), chCount("BANNER_HTTP"))
	if s := j.lastSub["BANNER_SSH"]; s != "" {
		bannerLive += "  last: " + s
	}
	return map[string]any{
		"01": st(fmt.Sprintf("vhost0 · %.0f/s denied across %d channels", tickTotal, len(j.series)), "ok"),
		"02": st(fmt.Sprintf("%d active probers", len(j.probers)), "ok"),
		"03": st(osfpLive, "ok"),
		"04": st(fmt.Sprintf("%.0f/s", chCount("SEQ_IPID")), "ok"),
		"05": st(fmt.Sprintf("%.0f/s", chCount("ICMP")), "ok"),
		"06": st(bannerLive, "ok"),
		"07": st(fmt.Sprintf("%d tarpitted", j.tarpit), tarpitStatus),
		"08": st(fmt.Sprintf("%d decoy touches", j.decoyHits), decoyStatus),
		"09": st(fmt.Sprintf("%d threat signals", j.threats), "ok"),
	}
}

// ---- helpers ----------------------------------------------------------

func hostOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[:i]
	}
	return addr
}

func describeEndpoint(kind, ns, name string) string {
	switch kind {
	case "pod", "service":
		if ns != "" {
			return ns + "/" + name
		}
		return name
	case "node":
		return "node/" + name
	default:
		return "external"
	}
}

// sanitizeSub renders a substitute payload for display: printable ASCII
// passes through; anything else (OSFP's raw [ttl,win] bytes arrive here
// as control chars) is shown as a short hex preview.
func sanitizeSub(s string) string {
	printable := true
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			printable = false
			break
		}
	}
	if printable {
		if len(s) > 40 {
			return s[:40]
		}
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s) && i < 8; i++ {
		fmt.Fprintf(&b, "%02x ", s[i])
	}
	return strings.TrimSpace(b.String())
}
