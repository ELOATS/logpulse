package pattern

import "testing"

// 典型消息：写入路径上每条日志都会模板化一次。
var benchMessages = []string{
	"request handled in 113ms",
	"GET /api/orders/57813 HTTP/1.1 status=200",
	"payment gateway timeout after 3333ms for order 73554",
	"connection to 10.1.2.3:5432 refused (attempt 2)",
	"reserved 5 items for sku 967",
	"cache miss storm for key user:4821",
	`failed to open file "/etc/app.conf": permission denied`,
	"request 3f2b1c9e-1a2b-4c3d-9e8f-0123456789ab failed",
	"TypeError: Cannot read properties of undefined (reading 'map')",
	"user logged out",
}

func BenchmarkTemplate(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Template(benchMessages[i%len(benchMessages)])
	}
}
