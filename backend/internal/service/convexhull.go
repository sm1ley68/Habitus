// convexhull.go — honest heuristic for suggested_areas_geojson, which ML's
// /search never returns. Convex hull of the result coordinates, buffered by a
// small fixed margin. Explicitly labeled `approximation: true` in output
// properties rather than presented as a real recommended-zone computation.
package service

import (
	"math"
	"sort"

	"habitus-backend/internal/geojson"
)

const bufferMeters = 150.0

// metersToDegLat/Lon are rough conversions good enough for a small visual
// buffer at Moscow's latitude — not geodesically exact, which is fine here.
func metersToDegLat(m float64) float64 { return m / 111_320.0 }
func metersToDegLon(m, lat float64) float64 {
	return m / (111_320.0 * math.Cos(lat*math.Pi/180))
}

type point struct{ x, y float64 } // x=lon, y=lat

func cross(o, a, b point) float64 {
	return (a.x-o.x)*(b.y-o.y) - (a.y-o.y)*(b.x-o.x)
}

// convexHull implements Andrew's monotone chain algorithm.
func convexHull(pts []point) []point {
	sort.Slice(pts, func(i, j int) bool {
		if pts[i].x != pts[j].x {
			return pts[i].x < pts[j].x
		}
		return pts[i].y < pts[j].y
	})
	uniq := pts[:0]
	for i, p := range pts {
		if i == 0 || p != pts[i-1] {
			uniq = append(uniq, p)
		}
	}
	pts = uniq
	n := len(pts)
	if n < 3 {
		return pts
	}

	hull := make([]point, 0, 2*n)
	for _, p := range pts {
		for len(hull) >= 2 && cross(hull[len(hull)-2], hull[len(hull)-1], p) <= 0 {
			hull = hull[:len(hull)-1]
		}
		hull = append(hull, p)
	}
	lower := len(hull) + 1
	for i := n - 2; i >= 0; i-- {
		p := pts[i]
		for len(hull) >= lower && cross(hull[len(hull)-2], hull[len(hull)-1], p) <= 0 {
			hull = hull[:len(hull)-1]
		}
		hull = append(hull, p)
	}
	return hull[:len(hull)-1]
}

func bboxSquare(center point, halfSizeM float64) []point {
	dLat := metersToDegLat(halfSizeM)
	dLon := metersToDegLon(halfSizeM, center.y)
	return []point{
		{center.x - dLon, center.y - dLat},
		{center.x + dLon, center.y - dLat},
		{center.x + dLon, center.y + dLat},
		{center.x - dLon, center.y + dLat},
	}
}

// bufferRing pushes each vertex outward from the ring's centroid by a fixed
// margin — a cheap, visually-adequate stand-in for a true polygon buffer.
func bufferRing(ring []point, meters float64) []point {
	if len(ring) == 0 {
		return ring
	}
	var cx, cy float64
	for _, p := range ring {
		cx += p.x
		cy += p.y
	}
	cx /= float64(len(ring))
	cy /= float64(len(ring))
	dLat := metersToDegLat(meters)
	dLon := metersToDegLon(meters, cy)

	out := make([]point, len(ring))
	for i, p := range ring {
		out[i] = point{p.x + math.Copysign(dLon, p.x-cx), p.y + math.Copysign(dLat, p.y-cy)}
	}
	return out
}

func closeRing(ring []point) [][2]float64 {
	out := make([][2]float64, 0, len(ring)+1)
	for _, p := range ring {
		out = append(out, [2]float64{p.x, p.y})
	}
	if len(ring) > 0 {
		out = append(out, [2]float64{ring[0].x, ring[0].y})
	}
	return out
}

// BuildSuggestedAreas returns the recommended-zone FeatureCollection for
// final_result. coords are [lng,lat] pairs of the result objects; point is the
// optional custom-point constraint from the request (used as a fallback
// center when there are no results at all).
func BuildSuggestedAreas(coords [][2]float64, customPoint *[2]float64) geojson.FeatureCollection {
	fc := geojson.NewFeatureCollection()
	props := map[string]any{"area_type": "recommended_zone", "approximation": true}

	if len(coords) == 0 {
		if customPoint == nil {
			return fc
		}
		ring := bboxSquare(toPoint(*customPoint), 300)
		fc.Features = append(fc.Features, geojson.Polygon(closeRing(ring), props))
		return fc
	}

	pts := make([]point, len(coords))
	for i, c := range coords {
		pts[i] = toPoint(c)
	}

	var ring []point
	if len(pts) < 3 {
		ring = bboxSquare(pts[0], 300)
	} else {
		ring = bufferRing(convexHull(pts), bufferMeters)
	}
	fc.Features = append(fc.Features, geojson.Polygon(closeRing(ring), props))
	return fc
}

func toPoint(c [2]float64) point { return point{x: c[0], y: c[1]} }
