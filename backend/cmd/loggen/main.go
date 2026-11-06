// loggen 生成用于演示的混合格式日志，并在中间注入一次故障（错误突增 + 新错误模式）。
//
//	go run ./cmd/loggen -minutes 120 > sample.log
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"time"
)

func main() {
	minutes := flag.Int("minutes", 120, "生成多少分钟的日志")
	rate := flag.Int("rate", 40, "每分钟基础日志条数")
	seed := flag.Int64("seed", 42, "随机种子")
	flag.Parse()

	rng := rand.New(rand.NewSource(*seed))
	start := time.Now().UTC().Add(-time.Duration(*minutes) * time.Minute).Truncate(time.Minute)
	incidentAt := *minutes * 3 / 4
	out := os.Stdout

	for m := 0; m < *minutes; m++ {
		minute := start.Add(time.Duration(m) * time.Minute)
		inIncident := m >= incidentAt && m < incidentAt+5
		n := *rate + rng.Intn(*rate/4+1)
		for i := 0; i < n; i++ {
			ts := minute.Add(time.Duration(rng.Intn(60000)) * time.Millisecond)
			switch rng.Intn(4) {
			case 0: // nginx 访问日志
				status := 200
				if r := rng.Intn(100); r < 3 {
					status = 404
				} else if r < 4 {
					status = 500
				}
				if inIncident && rng.Intn(3) == 0 {
					status = 502
				}
				fmt.Fprintf(out, "10.0.%d.%d - - [%s] \"GET /api/orders/%d HTTP/1.1\" %d %d \"-\" \"Mozilla/5.0\"\n",
					rng.Intn(255), rng.Intn(255), ts.Format("02/Jan/2006:15:04:05 -0700"), rng.Intn(99999), status, 200+rng.Intn(4000))
			case 1, 2: // JSON 结构化日志
				svc := []string{"order-service", "payment-service", "user-service"}[rng.Intn(3)]
				level, msg := "info", fmt.Sprintf("request handled in %dms", 5+rng.Intn(200))
				switch r := rng.Intn(100); {
				case r < 5:
					level, msg = "warn", fmt.Sprintf("slow query took %dms on table orders", 800+rng.Intn(2000))
				case r < 7:
					level, msg = "error", fmt.Sprintf("cache miss storm for key user:%d", rng.Intn(10000))
				}
				if inIncident && svc == "payment-service" {
					level = "error"
					msg = fmt.Sprintf("payment gateway timeout after %dms for order %d", 3000+rng.Intn(2000), rng.Intn(99999))
				}
				b, _ := json.Marshal(map[string]any{
					"ts": ts.Format(time.RFC3339Nano), "level": level, "service": svc, "msg": msg,
					"host": fmt.Sprintf("pod-%d", rng.Intn(4)), "trace_id": fmt.Sprintf("%016x", rng.Uint64()),
				})
				fmt.Fprintln(out, string(b))
			default: // 通用文本日志
				msg := fmt.Sprintf("INFO [inventory] reserved %d items for sku %d", 1+rng.Intn(5), rng.Intn(5000))
				if rng.Intn(50) == 0 {
					msg = fmt.Sprintf("WARN [inventory] stock low for sku %d remaining=%d", rng.Intn(5000), rng.Intn(10))
				}
				if inIncident && rng.Intn(2) == 0 {
					msg = fmt.Sprintf("ERROR [inventory] connection to 10.1.2.3:5432 refused (attempt %d)", 1+rng.Intn(5))
				}
				fmt.Fprintf(out, "%s %s\n", ts.Format("2006-01-02 15:04:05.000"), msg)
			}
		}
	}
}
