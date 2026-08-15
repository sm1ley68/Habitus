package main

import "testing"

func TestParsePriceRanges(t *testing.T) {
	t.Parallel()
	ranges, err := parsePriceRanges("0:10000000,10000001:0")
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 2 || ranges[0].maximum != 10_000_000 || ranges[1].minimum != 10_000_001 {
		t.Fatalf("ranges = %#v", ranges)
	}
}

func TestCollectProxiesDeduplicates(t *testing.T) {
	t.Parallel()
	proxies, err := collectProxies(
		[]string{"http://user:pass@proxy.test:8000"},
		"",
		"http://user:pass@proxy.test:8000,socks5://proxy-2.test:9000",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 2 {
		t.Fatalf("proxies = %#v", proxies)
	}
}
