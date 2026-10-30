package main

import (
	"flag"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"hapegateway/link"
)

type apiRequest struct {
	PlayerID    uint64 `json:"playerId"`
	Target      uint64 `json:"targetId"`
	Action      string `json:"action"`
	Gold        int64  `json:"gold"`
	TargetGold  int64  `json:"targetGold"`
	Power       int64  `json:"power"`
	TradeID     string `json:"tradeId"`
	K           int    `json:"k"`
	Season      uint32 `json:"season"`
	DefPower    int64  `json:"defPower"`
	BattleID    string `json:"battleId"`
	FromVersion uint64 `json:"fromVersion"`
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
	addr := flag.String("addr", "127.0.0.1:9001", "gateway TCP address")
	n := flag.Int("n", 1000, "number of bots")
	rate := flag.Float64("rate", 0.2, "actions per bot per second")
	duration := flag.Duration("duration", time.Hour, "test duration")
	flag.Parse()

	if *n <= 0 || *rate <= 0 || *duration <= 0 {
		panic("n, rate, duration must be positive")
	}
	if err := waitReady(*addr, 15*time.Second); err != nil {
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
			tr, err := dialTCP(*addr, uint64(id))
			if err != nil {
				st.record(0, err)
				return
			}
			defer tr.close()
			peers.Store(uint64(id), tr)
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
						_, err = tr.do(apiRequest{PlayerID: uint64(id), Action: "setpower", Power: int64(rng.Intn(100000))})
					case roll < 0.60:
						_, err = tr.do(apiRequest{PlayerID: uint64(id), Action: "grant", Gold: 10})
					case roll < 0.75:
						_, err = tr.do(apiRequest{PlayerID: uint64(id), Action: "rank"})
					case roll < 0.85:
						_, err = tr.do(apiRequest{PlayerID: uint64(id), Action: "top", K: 10})
					case id%50 == 0:
						err = tradePeers(uint64(id), peer)
					default:
						_, err = tr.do(apiRequest{PlayerID: uint64(id), Action: "rank"})
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
	samples := append([]string(nil), st.samples...)
	st.mu.Unlock()
	sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
	p50 := lats[len(lats)*50/100]
	p99 := lats[len(lats)*99/100]
	fmt.Printf("bots=%d total=%d ok=%d err=%d err_rate=%.4f p50=%dus p99=%dus\n",
		*n, total, ok, bad, float64(bad)/float64(total), p50, p99)
	for _, s := range samples {
		fmt.Printf("err-sample: %s\n", s)
	}
}

func waitReady(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := link.Dial(addr, 500*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("gateway not ready after %s", timeout)
}
