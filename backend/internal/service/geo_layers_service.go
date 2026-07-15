// geo_layers_service.go — GET /geo/layers. See plan §6 for exactly which of
// the 6 enum layers are backed by real data today.
package service

import (
	"context"

	"habitus-backend/internal/domain"
	"habitus-backend/internal/geojson"
)

// AllowedLayers is the closed enum from frontend/Пайплайн фронт.md §5.
// Unknown values are silently dropped by the handler, per spec.
var AllowedLayers = map[string]bool{
	"communal": true, "noise": true, "schools": true,
	"bars": true, "ecology": true, "parks": true, "metro": true,
}

// layerKinds maps a frontend layer name to the poi.kind values that back it.
// communal/noise/ecology have no source anywhere in the pipeline today — they
// intentionally have no entry here and always resolve to an empty
// FeatureCollection (see plan §6: fabricating geometry for them would be
// actively misleading, e.g. noise_level is a bar-density proxy, not traffic
// noise).
var layerKinds = map[string][]string{
	"schools": {"school"},
	"bars":    {"bar", "alcohol"},
	"parks":   {"park"},
	"metro":   {"metro"},
}

type poiLister interface {
	ListByKinds(ctx context.Context, kinds []string) ([]domain.POI, error)
}

type GeoLayersService struct {
	pois poiLister
}

func NewGeoLayersService(pois poiLister) *GeoLayersService {
	return &GeoLayersService{pois: pois}
}

// Layers returns a FeatureCollection per requested (and recognized) layer
// name. `city` is accepted by the handler but not filtered on here — the
// entire poi/listings dataset is Moscow-only and neither table has a city
// column (flagged as a data/ingestion gap, not something Go can fix).
func (s *GeoLayersService) Layers(ctx context.Context, requested []string) (map[string]geojson.FeatureCollection, error) {
	out := make(map[string]geojson.FeatureCollection)

	var kindsToFetch []string
	layersNeedingKinds := map[string][]string{}
	for _, layer := range requested {
		if !AllowedLayers[layer] {
			continue
		}
		kinds, ok := layerKinds[layer]
		if !ok {
			out[layer] = geojson.NewFeatureCollection()
			continue
		}
		layersNeedingKinds[layer] = kinds
		kindsToFetch = append(kindsToFetch, kinds...)
	}

	if len(kindsToFetch) == 0 {
		return out, nil
	}

	pois, err := s.pois.ListByKinds(ctx, kindsToFetch)
	if err != nil {
		return nil, err
	}
	byKind := make(map[string][]domain.POI)
	for _, p := range pois {
		byKind[p.Kind] = append(byKind[p.Kind], p)
	}

	for layer, kinds := range layersNeedingKinds {
		fc := geojson.NewFeatureCollection()
		for _, kind := range kinds {
			for _, p := range byKind[kind] {
				props := map[string]any{"kind": p.Kind}
				if p.Name != "" {
					props["name"] = p.Name
				}
				fc.Features = append(fc.Features, geojson.Point(p.Lon, p.Lat, props))
			}
		}
		out[layer] = fc
	}
	return out, nil
}
