package pattern

import "testing"

func TestTemplate(t *testing.T) {
	cases := map[string]string{
		"User 12345 logged in from 10.0.0.1":                     "User <NUM> logged in from <IP>",
		"User 99 logged in from 192.168.1.20:8080":               "User <NUM> logged in from <IP>",
		"GET /api/users/42/orders took 35ms":                     "GET /api/users/<NUM>/orders took <NUM>",
		"request 3f2b1c9e-1a2b-4c3d-9e8f-0123456789ab failed":    "request <UUID> failed",
		`failed to open file "/etc/app.conf": permission denied`: `failed to open file "<STR>": permission denied`,
		"txn deadbeef1234 committed at 2024-05-01T10:00:00Z":     "txn <HEX> committed at <TIME>",
		"GET /api/orders/123 HTTP/1.1 status=502":                "GET /api/orders/<NUM> HTTP/1.1 status=502",
		"retry 1 2 3 times":                                      "retry <NUM>+ times",
		"connection to https://db.internal:5432/main refused":    "connection to <URL> refused",
	}
	for in, want := range cases {
		if got := Template(in); got != want {
			t.Errorf("Template(%q)\n got  %q\n want %q", in, got, want)
		}
	}
}

func TestSameTemplateSameHash(t *testing.T) {
	a := Template("Payment 1001 failed for user 7")
	b := Template("Payment 2002 failed for user 8")
	if Hash("pay", "ERROR", a) != Hash("pay", "ERROR", b) {
		t.Fatalf("expected same hash: %q vs %q", a, b)
	}
	if Hash("pay", "ERROR", a) == Hash("other", "ERROR", a) {
		t.Fatal("different services should hash differently")
	}
}
