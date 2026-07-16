package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"habitus-backend/internal/cian"
)

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }
func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

type options struct {
	output           string
	format           string
	rooms            string
	priceRanges      string
	pages            int
	maxOffers        int
	region           int
	proxies          stringList
	proxyFile        string
	allowDirect      bool
	timeout          time.Duration
	delayMin         time.Duration
	delayMax         time.Duration
	retries          int
	bootstrapCookies bool
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmsgprefix)
	log.SetPrefix("cian-parser: ")

	config, err := parseFlags()
	if err != nil {
		log.Fatal(err)
	}
	if err := run(config); err != nil {
		log.Fatal(err)
	}
}

func parseFlags() (options, error) {
	var config options
	flag.StringVar(&config.output, "output", "../data/cian/listings.csv", "output .csv or .json path")
	flag.StringVar(&config.format, "format", "auto", "output format: auto, csv, json")
	flag.StringVar(&config.rooms, "rooms", "1,2,3,4", "comma-separated room filters")
	flag.StringVar(&config.priceRanges, "price-ranges", "", "comma-separated min:max price windows; 0 means open bound")
	flag.IntVar(&config.pages, "pages", 54, "maximum pages for each filter")
	flag.IntVar(&config.maxOffers, "max-offers", 5000, "stop after this many unique offers")
	flag.IntVar(&config.region, "region", 1, "Cian region id (1 is Moscow)")
	flag.Var(&config.proxies, "proxy", "proxy URL; repeat for rotation (http://user:pass@host:port or socks5://...)")
	flag.StringVar(&config.proxyFile, "proxy-file", "", "file with one proxy URL per line")
	flag.BoolVar(&config.allowDirect, "allow-direct", false, "allow a direct connection when no proxy is configured")
	flag.DurationVar(&config.timeout, "timeout", 30*time.Second, "timeout for one HTTP request")
	flag.DurationVar(&config.delayMin, "delay-min", 3*time.Second, "minimum delay between requests")
	flag.DurationVar(&config.delayMax, "delay-max", 6*time.Second, "maximum delay between requests")
	flag.IntVar(&config.retries, "retries", 3, "retries with session/proxy rotation after a block or network failure")
	flag.BoolVar(&config.bootstrapCookies, "bootstrap-cookies", true, "visit cian.ru first and retain session cookies")
	flag.Parse()

	if config.pages < 1 || config.pages > 54 {
		return config, errors.New("--pages must be between 1 and Cian's 54-page window")
	}
	if config.maxOffers < 1 {
		return config, errors.New("--max-offers must be positive")
	}
	if config.region < 1 {
		return config, errors.New("--region must be positive")
	}
	if config.timeout <= 0 {
		return config, errors.New("--timeout must be positive")
	}
	if config.retries < 0 {
		return config, errors.New("--retries cannot be negative")
	}
	return config, nil
}

func run(config options) error {
	rooms, err := parsePositiveInts(config.rooms)
	if err != nil {
		return fmt.Errorf("--rooms: %w", err)
	}
	ranges, err := parsePriceRanges(config.priceRanges)
	if err != nil {
		return fmt.Errorf("--price-ranges: %w", err)
	}
	filters := buildFilters(rooms, ranges)

	proxies, err := collectProxies(config.proxies, config.proxyFile, os.Getenv("CIAN_PROXIES"))
	if err != nil {
		return err
	}
	if len(proxies) == 0 {
		if !config.allowDirect {
			return errors.New("no proxy configured; pass --proxy/--proxy-file or CIAN_PROXIES (use --allow-direct only for diagnostics)")
		}
		proxies = []string{""}
		log.Print("warning: using a direct connection; Cian commonly returns captcha for it")
	}

	pause, err := cian.RandomPause(config.delayMin, config.delayMax)
	if err != nil {
		return err
	}
	store, err := cian.OpenStore(config.output, cian.OutputFormat(config.format))
	if err != nil {
		return err
	}
	initialRows := store.Len()

	sessionConfig := cian.SessionConfig{
		Region:           config.region,
		BootstrapCookies: config.bootstrapCookies,
		BetweenRequests:  pause,
	}
	factory := func(proxyURL string) (cian.SearchSession, error) {
		return cian.NewTLSSession(proxyURL, config.timeout, sessionConfig)
	}
	pool, err := cian.NewPool(cian.PoolConfig{
		Proxies: proxies,
		Retries: config.retries,
		Factory: factory,
		Backoff: pause,
	})
	if err != nil {
		return err
	}
	defer pool.Close()

	scraper, err := cian.NewScraper(cian.ScraperConfig{
		Filters:      filters,
		Pages:        config.pages,
		MaxOffers:    config.maxOffers,
		BetweenPages: pause,
	}, pool)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("start: filters=%d proxies=%d existing_rows=%d output=%s", len(filters), len(proxies), initialRows, config.output)
	progress, err := scraper.Run(ctx, func(offers []cian.Listing, progress cian.Progress) error {
		inserted, updated := store.Merge(offers)
		if len(offers) > 0 {
			if err := store.Save(); err != nil {
				return err
			}
		}
		log.Printf(
			"room=%d price=%d:%d page=%d received=%d new=%d updated=%d run_unique=%d total=%d",
			progress.Filter.Room, progress.Filter.MinPrice, progress.Filter.MaxPrice,
			progress.Page, progress.PageOffers, inserted, updated, progress.UniqueOffers, store.Len(),
		)
		return nil
	})
	if err != nil {
		return err
	}
	log.Printf("done: pages=%d run_unique=%d total_rows=%d added_to_snapshot=%d", progress.CompletedPages, progress.UniqueOffers, store.Len(), store.Len()-initialRows)
	return nil
}

func parsePositiveInts(value string) ([]int, error) {
	parts := strings.Split(value, ",")
	result := make([]int, 0, len(parts))
	seen := make(map[int]struct{}, len(parts))
	for _, part := range parts {
		number, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || number < 1 {
			return nil, fmt.Errorf("invalid positive integer %q", part)
		}
		if _, exists := seen[number]; exists {
			continue
		}
		seen[number] = struct{}{}
		result = append(result, number)
	}
	if len(result) == 0 {
		return nil, errors.New("at least one room value is required")
	}
	return result, nil
}

type priceRange struct {
	minimum int64
	maximum int64
}

func parsePriceRanges(value string) ([]priceRange, error) {
	if strings.TrimSpace(value) == "" {
		return []priceRange{{}}, nil
	}
	parts := strings.Split(value, ",")
	result := make([]priceRange, 0, len(parts))
	for _, part := range parts {
		bounds := strings.Split(strings.TrimSpace(part), ":")
		if len(bounds) != 2 {
			return nil, fmt.Errorf("invalid range %q; expected min:max", part)
		}
		minimum, err := strconv.ParseInt(strings.TrimSpace(bounds[0]), 10, 64)
		if err != nil || minimum < 0 {
			return nil, fmt.Errorf("invalid minimum in %q", part)
		}
		maximum, err := strconv.ParseInt(strings.TrimSpace(bounds[1]), 10, 64)
		if err != nil || maximum < 0 {
			return nil, fmt.Errorf("invalid maximum in %q", part)
		}
		if maximum > 0 && minimum > maximum {
			return nil, fmt.Errorf("minimum exceeds maximum in %q", part)
		}
		result = append(result, priceRange{minimum: minimum, maximum: maximum})
	}
	return result, nil
}

func buildFilters(rooms []int, ranges []priceRange) []cian.Filter {
	filters := make([]cian.Filter, 0, len(rooms)*len(ranges))
	for _, room := range rooms {
		for _, price := range ranges {
			filters = append(filters, cian.Filter{Room: room, MinPrice: price.minimum, MaxPrice: price.maximum})
		}
	}
	return filters
}

func collectProxies(flags []string, path, environment string) ([]string, error) {
	values := append([]string(nil), flags...)
	values = append(values, splitProxyValues(environment)...)
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open proxy file: %w", err)
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" && !strings.HasPrefix(line, "#") {
				values = append(values, line)
			}
		}
		closeErr := file.Close()
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("read proxy file: %w", err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close proxy file: %w", closeErr)
		}
	}

	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "socks5") {
			return nil, errors.New("invalid proxy URL; expected http://user:pass@host:port, https://..., or socks5://...")
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func splitProxyValues(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})
}
