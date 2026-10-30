package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type apiRequest struct {
	PlayerID   uint64 `json:"playerId"`
	Target     uint64 `json:"targetId"`
	Action     string `json:"action"`
	Gold       int64  `json:"gold"`
	TargetGold int64  `json:"targetGold"`
	Power      int64  `json:"power"`
	TradeID    string `json:"tradeId"`
	K          int    `json:"k"`
}

type apiResponse struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
}

type stats struct {
	ok      atomic.Int64
	err     atomic.Int64
	mu      sync.Mutex
	lat     []int64
	samples []string
}

func (s *stats) record(d time.Duration, err error) {
	if err != nil {
		s.err.Add(1)
	} else {
		s.ok.Add(1)
	}
	s.mu.Lock()
	s.lat = append(s.lat, d.Microseconds())
	if err != nil && len(s.samples) < 10 {
		s.samples = append(s.samples, err.Error())
	}
	s.mu.Unlock()
}

func main() {
	base := flag.String("base", "http://127.0.0.1:8080", "world HTTP base URL")
	n := flag.Int("n", 1000, "number of bots")
	rate := flag.Float64("rate", 0.2, "actions per bot per second")
	duration := flag.Duration("duration", time.Hour, "test duration")
	flag.Parse()

	if *n <= 0 || *rate <= 0 || *duration <= 0 {
		panic("n, rate, duration must be positive")
	}

	transport := &http.Transport{
		MaxIdleConns:        4000,
		MaxIdleConnsPerHost: 4000,
		IdleConnTimeout:     60 * time.Second,
	}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	if err := waitReady(client, *base, 15*time.Second); err != nil {
		panic(err)
	}
	st := &stats{}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	interval := time.Duration(float64(time.Second) / *rate)

	for i := 1; i <= *n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id)))
			time.Sleep(time.Duration(rng.Intn(5000)) * time.Millisecond)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			peer := uint64(id%*n + 1)
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					start := time.Now()
					var err error
					roll := rng.Float64()
					switch {
					case roll < 0.45:
						err = call(client, *base, apiRequest{PlayerID: uint64(id), Action: "setpower", Power: int64(rng.Intn(100000))})
					case roll < 0.60:
						err = call(client, *base, apiRequest{PlayerID: uint64(id), Action: "grant", Gold: 10})
					case roll < 0.75:
						err = call(client, *base, apiRequest{PlayerID: uint64(id), Action: "rank"})
					case roll < 0.85:
						err = call(client, *base, apiRequest{PlayerID: uint64(id), Action: "top", K: 10})
					case id%50 == 0:
						err = trade(client, *base, uint64(id), peer)
					default:
						err = call(client, *base, apiRequest{PlayerID: uint64(id), Action: "rank"})
					}
					st.record(time.Since(start), err)
				}
			}
		}(i)
	}

	time.Sleep(*duration)
	close(stop)
	wg.Wait()

	ok, bad := st.ok.Load(), st.err.Load()
	total := ok + bad
	if total == 0 {
		fmt.Println("no requests")
		return
	}
	st.mu.Lock()
	lats := append([]int64(nil), st.lat...)
	st.mu.Unlock()
	sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
	p50 := lats[len(lats)*50/100]
	p99 := lats[len(lats)*99/100]
	fmt.Printf("bots=%d total=%d ok=%d err=%d err_rate=%.4f p50=%dus p99=%dus\n",
		*n, total, ok, bad, float64(bad)/float64(total), p50, p99)
	for _, s := range st.samples {
		fmt.Printf("err-sample: %s\n", s)
	}
}

func call(c *http.Client, base string, req apiRequest) error {
	body, _ := json.Marshal(req)
	resp, err := c.Post(base+"/api/command", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var out apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if !out.OK {
		return fmt.Errorf("%s", out.Error)
	}
	return nil
}

func trade(c *http.Client, base string, a, b uint64) error {
	body, _ := json.Marshal(apiRequest{
		PlayerID: a, Target: b, Action: "trade_create", Gold: 1, TargetGold: 0,
	})
	resp, err := c.Post(base+"/api/command", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var created apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return err
	}
	if !created.OK {
		return fmt.Errorf("create: %s", created.Error)
	}
	var tr struct {
		ID string `json:"_id"`
	}
	if err := json.Unmarshal(created.Data, &tr); err != nil || tr.ID == "" {
		return fmt.Errorf("create: bad trade id")
	}
	if err := call(c, base, apiRequest{PlayerID: b, Action: "trade_accept", TradeID: tr.ID}); err != nil {
		return err
	}
	return call(c, base, apiRequest{PlayerID: a, Action: "trade_settle", TradeID: tr.ID})
}

func waitReady(c *http.Client, base string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := c.Get(base + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("world not ready after %s", timeout)
}
