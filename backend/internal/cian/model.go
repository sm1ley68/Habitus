package cian

import "time"

// Metro describes one underground station attached to a Cian offer.
type Metro struct {
	Name          string `json:"name"`
	Time          *int   `json:"time,omitempty"`
	TransportType string `json:"transport_type,omitempty"`
}

// Listing is the stable, source-oriented representation written by the parser.
// Pointers preserve the distinction between a missing numeric field and zero.
type Listing struct {
	CianID             string    `json:"cian_id"`
	Description        string    `json:"description"`
	Price              *int64    `json:"price,omitempty"`
	Area               *float64  `json:"area,omitempty"`
	Rooms              *int      `json:"rooms,omitempty"`
	Floor              *int      `json:"floor,omitempty"`
	Floors             *int      `json:"floors,omitempty"`
	Address            string    `json:"address"`
	Metro              []Metro   `json:"metro"`
	ResidentialComplex string    `json:"zhk"`
	BuildingMaterial   string    `json:"building_material,omitempty"`
	Deadline           string    `json:"deadline,omitempty"`
	Latitude           *float64  `json:"latitude,omitempty"`
	Longitude          *float64  `json:"longitude,omitempty"`
	URL                string    `json:"url,omitempty"`
	CollectedAt        time.Time `json:"collected_at"`
}

// Filter is one independent Cian result window. Combining room and price
// filters lets callers stay below Cian's per-query result limit.
type Filter struct {
	Room     int
	MinPrice int64
	MaxPrice int64
}
