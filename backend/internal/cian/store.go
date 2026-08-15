package cian

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type OutputFormat string

const (
	FormatCSV  OutputFormat = "csv"
	FormatJSON OutputFormat = "json"
)

var csvHeader = []string{
	"cian_id", "description", "price", "area", "rooms", "floor", "floors",
	"address", "metro", "zhk", "building_material", "deadline", "latitude",
	"longitude", "url", "collected_at",
}

type Store struct {
	path   string
	format OutputFormat
	items  []Listing
	index  map[string]int
}

func OpenStore(path string, format OutputFormat) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("output path is required")
	}
	resolved, err := ResolveOutputFormat(path, format)
	if err != nil {
		return nil, err
	}
	store := &Store{path: path, format: resolved, index: make(map[string]int)}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func ResolveOutputFormat(path string, format OutputFormat) (OutputFormat, error) {
	if format == "" || format == "auto" {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".csv":
			return FormatCSV, nil
		case ".json":
			return FormatJSON, nil
		default:
			return "", errors.New("cannot infer output format; use --format csv or --format json")
		}
	}
	if format != FormatCSV && format != FormatJSON {
		return "", fmt.Errorf("unsupported output format %q", format)
	}
	return format, nil
}

func (store *Store) load() error {
	file, err := os.Open(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open existing output: %w", err)
	}
	defer file.Close()

	var items []Listing
	if store.format == FormatCSV {
		items, err = readCSV(file)
	} else {
		decoder := json.NewDecoder(file)
		err = decoder.Decode(&items)
		if errors.Is(err, io.EOF) {
			err = nil
		}
	}
	if err != nil {
		return fmt.Errorf("read existing output: %w", err)
	}
	for _, item := range items {
		if item.CianID == "" {
			continue
		}
		if index, exists := store.index[item.CianID]; exists {
			store.items[index] = item
			continue
		}
		store.index[item.CianID] = len(store.items)
		store.items = append(store.items, item)
	}
	return nil
}

// Merge performs latest-wins upserts by stable Cian id.
func (store *Store) Merge(items []Listing) (inserted, updated int) {
	for _, item := range items {
		if item.CianID == "" {
			continue
		}
		if index, exists := store.index[item.CianID]; exists {
			store.items[index] = item
			updated++
			continue
		}
		store.index[item.CianID] = len(store.items)
		store.items = append(store.items, item)
		inserted++
	}
	return inserted, updated
}

func (store *Store) Len() int { return len(store.items) }

// Save rewrites the small (1-5k row) snapshot atomically, so an interrupted
// run never leaves a half-written CSV/JSON file.
func (store *Store) Save() error {
	directory := filepath.Dir(store.path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".cian-output-*")
	if err != nil {
		return fmt.Errorf("create temporary output: %w", err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	if store.format == FormatCSV {
		err = writeCSV(temporary, store.items)
	} else {
		encoder := json.NewEncoder(temporary)
		encoder.SetIndent("", "  ")
		err = encoder.Encode(store.items)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	if err := os.Chmod(temporaryPath, 0o644); err != nil {
		return fmt.Errorf("set output permissions: %w", err)
	}
	if err := os.Rename(temporaryPath, store.path); err != nil {
		return fmt.Errorf("replace output atomically: %w", err)
	}
	removeTemporary = false
	return nil
}

func writeCSV(writer io.Writer, items []Listing) error {
	csvWriter := csv.NewWriter(writer)
	if err := csvWriter.Write(csvHeader); err != nil {
		return err
	}
	for _, item := range items {
		metroJSON, err := json.Marshal(item.Metro)
		if err != nil {
			return err
		}
		record := []string{
			item.CianID,
			item.Description,
			formatInt64(item.Price),
			formatFloat(item.Area),
			formatInt(item.Rooms),
			formatInt(item.Floor),
			formatInt(item.Floors),
			item.Address,
			string(metroJSON),
			item.ResidentialComplex,
			item.BuildingMaterial,
			item.Deadline,
			formatFloat(item.Latitude),
			formatFloat(item.Longitude),
			item.URL,
			item.CollectedAt.UTC().Format(time.RFC3339),
		}
		if err := csvWriter.Write(record); err != nil {
			return err
		}
	}
	csvWriter.Flush()
	return csvWriter.Error()
}

func readCSV(reader io.Reader) ([]Listing, error) {
	csvReader := csv.NewReader(reader)
	records, err := csvReader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	positions := make(map[string]int, len(records[0]))
	for index, name := range records[0] {
		positions[name] = index
	}
	if _, ok := positions["cian_id"]; !ok {
		return nil, errors.New("CSV does not contain cian_id column")
	}
	items := make([]Listing, 0, len(records)-1)
	for rowNumber, record := range records[1:] {
		value := func(name string) string {
			index, ok := positions[name]
			if !ok || index >= len(record) {
				return ""
			}
			return record[index]
		}
		item := Listing{
			CianID:             value("cian_id"),
			Description:        value("description"),
			Price:              parseInt64(value("price")),
			Area:               parseFloat(value("area")),
			Rooms:              parseInt(value("rooms")),
			Floor:              parseInt(value("floor")),
			Floors:             parseInt(value("floors")),
			Address:            value("address"),
			ResidentialComplex: value("zhk"),
			BuildingMaterial:   value("building_material"),
			Deadline:           value("deadline"),
			Latitude:           parseFloat(value("latitude")),
			Longitude:          parseFloat(value("longitude")),
			URL:                value("url"),
		}
		if text := value("metro"); text != "" {
			if err := json.Unmarshal([]byte(text), &item.Metro); err != nil {
				return nil, fmt.Errorf("row %d metro: %w", rowNumber+2, err)
			}
		}
		if item.Metro == nil {
			item.Metro = []Metro{}
		}
		if text := value("collected_at"); text != "" {
			item.CollectedAt, err = time.Parse(time.RFC3339, text)
			if err != nil {
				return nil, fmt.Errorf("row %d collected_at: %w", rowNumber+2, err)
			}
		}
		items = append(items, item)
	}
	return items, nil
}

func formatInt(value *int) string {
	if value == nil {
		return ""
	}
	return strconv.Itoa(*value)
}

func formatInt64(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}

func formatFloat(value *float64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}

func parseInt(value string) *int {
	if value == "" {
		return nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return nil
	}
	return &parsed
}

func parseInt64(value string) *int64 {
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func parseFloat(value string) *float64 {
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil
	}
	return &parsed
}
