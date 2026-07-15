package handlers

import "testing"

func float64Ptr(v float64) *float64 { return &v }
func intPtr(v int) *int             { return &v }
func stringPtr(v string) *string    { return &v }

func TestNormalizePointOmitted(t *testing.T) {
	got, err := normalizePoint(nil)
	if err != nil || got != nil {
		t.Fatalf("normalizePoint(nil) = %#v, %v; want nil, nil", got, err)
	}
}

func TestNormalizePointDefaults(t *testing.T) {
	got, err := normalizePoint(&streamPointRequest{
		Lon: float64Ptr(37.6),
		Lat: float64Ptr(55.7),
	})
	if err != nil {
		t.Fatalf("normalizePoint() error = %v", err)
	}
	if got.Lon != 37.6 || got.Lat != 55.7 || got.Minutes != 15 || got.Mode != "foot-walking" {
		t.Fatalf("normalizePoint() = %#v; defaults were not applied", got)
	}
}

func TestNormalizePointFull(t *testing.T) {
	got, err := normalizePoint(&streamPointRequest{
		Lon:     float64Ptr(30.3),
		Lat:     float64Ptr(59.9),
		Minutes: intPtr(25),
		Mode:    stringPtr("cycling-regular"),
	})
	if err != nil {
		t.Fatalf("normalizePoint() error = %v", err)
	}
	if got.Minutes != 25 || got.Mode != "cycling-regular" {
		t.Fatalf("normalizePoint() = %#v", got)
	}
}

func TestNormalizePointRejectsInvalidValues(t *testing.T) {
	tests := map[string]*streamPointRequest{
		"missing longitude": {Lat: float64Ptr(55.7)},
		"missing latitude":  {Lon: float64Ptr(37.6)},
		"longitude":         {Lon: float64Ptr(181), Lat: float64Ptr(55.7)},
		"latitude":          {Lon: float64Ptr(37.6), Lat: float64Ptr(-91)},
		"minutes":           {Lon: float64Ptr(37.6), Lat: float64Ptr(55.7), Minutes: intPtr(0)},
		"mode":              {Lon: float64Ptr(37.6), Lat: float64Ptr(55.7), Mode: stringPtr("rocket")},
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizePoint(input); err == nil {
				t.Fatal("normalizePoint() error = nil; want validation error")
			}
		})
	}
}
