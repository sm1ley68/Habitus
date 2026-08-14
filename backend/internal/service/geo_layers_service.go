// geo_layers_service.go — GET /geo/layers. See plan §6 for exactly which of
// the 6 enum layers are backed by real data today.
package service

import (
	"context"

	"habitus-backend/internal/domain"
	"habitus-backend/internal/geojson"
)

// AllowedLayers — закрытый enum из frontend/Пайплайн фронт.md §5.
// ecology убран: источника под экологию нет ни в poi, ни в urban_evidence
// (CHECK допускает только communal/crime/noise). crime, наоборот, уже импортирован.
var AllowedLayers = map[string]bool{
	"communal": true, "noise": true, "schools": true,
	"bars": true, "crime": true, "parks": true, "metro": true,
}

// layerKinds maps a frontend layer name to the poi.kind values that back it.
var layerKinds = map[string][]string{
	"schools": {"school"},
	"bars":    {"bar", "alcohol"},
	"parks":   {"park"},
	"metro":   {"metro"},
}

// Слои из urban_evidence. Все три — МОДЕЛЬНЫЕ прокси, не замеры, поэтому
// источник едет наружу в properties каждой фичи.
var evidenceLayers = map[string]bool{"communal": true, "noise": true, "crime": true}

const (
	evidenceFeatureLimit = 5000
	evidenceSourceLabel  = "source"
)

type poiLister interface {
	ListByKinds(ctx context.Context, kinds []string) ([]domain.POI, error)
}

type evidenceLister interface {
	ListByLayers(ctx context.Context, city string, layers []string,
		bbox [4]float64, limit int) ([]domain.EvidenceFeature, error)
}

type GeoLayersService struct {
	pois     poiLister
	evidence evidenceLister
}

func NewGeoLayersService(pois poiLister, evidence evidenceLister) *GeoLayersService {
	return &GeoLayersService{pois: pois, evidence: evidence}
}

// Layers returns a FeatureCollection per requested (and recognized) layer
// name, плюс карту усечённых слоёв. Слои из urban_evidence скоупятся по городу
// и вьюпорту; POI-слои пока московские (в poi city появился, но выборка идёт по
// kind — сужение по городу отдельной задачей).
func (s *GeoLayersService) Layers(ctx context.Context, city string, requested []string,
	bbox *[4]float64) (map[string]geojson.FeatureCollection, map[string]bool, error) {
	out := make(map[string]geojson.FeatureCollection)
	truncated := make(map[string]bool)

	var evidenceWanted []string
	var kindsToFetch []string
	layersNeedingKinds := map[string][]string{}
	for _, layer := range requested {
		if !AllowedLayers[layer] {
			continue
		}
		if evidenceLayers[layer] {
			// Без вьюпорта отдавать нечего: сырой слой шума — это 46 335 линий.
			// Пустой слой — честный ответ «нет данных», а не 500.
			out[layer] = geojson.NewFeatureCollection()
			if bbox != nil {
				evidenceWanted = append(evidenceWanted, layer)
			}
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

	if len(evidenceWanted) > 0 {
		rows, err := s.evidence.ListByLayers(ctx, city, evidenceWanted, *bbox, evidenceFeatureLimit)
		if err != nil {
			return nil, nil, err
		}
		perLayer := map[string]int{}
		for _, row := range rows {
			// репозиторий берёт limit+1 на слой — перебор и есть признак усечения
			if perLayer[row.Layer] >= evidenceFeatureLimit {
				truncated[row.Layer] = true
				continue
			}
			perLayer[row.Layer]++
			props := map[string]any{"layer": row.Layer, evidenceSourceLabel: row.Source}
			if row.Weight != nil {
				props["weight"] = *row.Weight
			}
			if row.DB != nil {
				props["db"] = *row.DB
			}
			fc := out[row.Layer]
			fc.Features = append(fc.Features, geojson.RawFeature(row.GeometryJSON, props))
			out[row.Layer] = fc
		}
	}

	if len(kindsToFetch) == 0 {
		return out, truncated, nil
	}

	pois, err := s.pois.ListByKinds(ctx, kindsToFetch)
	if err != nil {
		return nil, nil, err
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
	return out, truncated, nil
}
