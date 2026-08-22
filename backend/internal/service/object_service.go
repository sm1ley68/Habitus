// object_service.go — GET /objects/{id}?chat_id=. Static listing fields come
// from Postgres; a query-specific dossier is lazily generated once and cached
// on the latest chat_search_result row.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/client"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/observability"
	"habitus-backend/internal/repository"
)

const DossierSchemaVersion = "dossier-v1"

type BriefStatus string
type BlockTier string
type LifestyleIcon string
type Grade string
type DestinationKind string
type TravelMode string
type LegSafety string
type SocialLayer string
type ViewType string

const (
	BriefMet        BriefStatus = "met"
	BriefCompromise BriefStatus = "compromise"
	BriefRelaxed    BriefStatus = "relaxed"
	BriefUnknown    BriefStatus = "unknown"

	TierHero      BlockTier = "hero"
	TierSecondary BlockTier = "secondary"

	IconSchool   LifestyleIcon = "school"
	IconUsers    LifestyleIcon = "users"
	IconSun      LifestyleIcon = "sun"
	IconVolume   LifestyleIcon = "volume"
	IconLeaf     LifestyleIcon = "leaf"
	IconHospital LifestyleIcon = "hospital"
	IconRoute    LifestyleIcon = "route"

	GradeAPlus  Grade = "A+"
	GradeA      Grade = "A"
	GradeAMinus Grade = "A-"
	GradeBPlus  Grade = "B+"
	GradeB      Grade = "B"
	GradeBMinus Grade = "B-"
	GradeCPlus  Grade = "C+"
	GradeC      Grade = "C"
	GradeCMinus Grade = "C-"
	GradeD      Grade = "D"

	DestinationSchool DestinationKind = "school"
	DestinationMetro  DestinationKind = "metro"
	DestinationWork   DestinationKind = "work"
	DestinationPark   DestinationKind = "park"
	DestinationPOI    DestinationKind = "poi"

	ModeWalk    TravelMode = "walk"
	ModeScooter TravelMode = "scooter"
	ModeBus     TravelMode = "bus"
	ModeCar     TravelMode = "car"
	ModeMetro   TravelMode = "metro"

	SafetySafe    LegSafety = "safe"
	SafetyCaution LegSafety = "caution"

	LayerCommunal SocialLayer = "communal"
	LayerBars     SocialLayer = "bars"
	LayerCrime    SocialLayer = "crime"

	ViewCourtyardPark ViewType = "courtyard_park"
	ViewStreet        ViewType = "street"
	ViewWater         ViewType = "water"
	ViewWall          ViewType = "wall"
	ViewWell          ViewType = "well"
)

var ValidBlockTiers = map[BlockTier]bool{TierHero: true, TierSecondary: true}
var ValidHeroKeys = map[string]bool{
	"family_routing": true, "social_environment": true, "view_and_climate": true,
}

type Block struct {
	Key         string         `json:"key"`
	Tier        BlockTier      `json:"tier,omitempty"`
	Title       string         `json:"title"`
	Icon        LifestyleIcon  `json:"icon,omitempty"`
	Score       Grade          `json:"score"`
	VerdictLine string         `json:"verdict_line,omitempty"`
	Description string         `json:"description"`
	Metrics     map[string]any `json:"metrics,omitempty"`
	Data        any            `json:"data,omitempty"`
}

func (b *Block) UnmarshalJSON(data []byte) error {
	var raw struct {
		Key         string          `json:"key"`
		Tier        string          `json:"tier"`
		Title       string          `json:"title"`
		Icon        string          `json:"icon"`
		Score       string          `json:"score"`
		VerdictLine string          `json:"verdict_line"`
		Description string          `json:"description"`
		Metrics     map[string]any  `json:"metrics"`
		Data        json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	b.Key, b.Tier, b.Title = raw.Key, BlockTier(raw.Tier), raw.Title
	b.Icon, b.Score = LifestyleIcon(raw.Icon), Grade(raw.Score)
	b.VerdictLine, b.Description = raw.VerdictLine, raw.Description
	b.Metrics = raw.Metrics
	if len(raw.Data) == 0 || string(raw.Data) == "null" {
		b.Data = nil
		return nil
	}
	var target any
	switch raw.Key {
	case "family_routing":
		target = &FamilyRoutingData{}
	case "social_environment":
		target = &SocialEnvironmentData{}
	case "view_and_climate":
		target = &ViewClimateData{}
	default:
		target = &map[string]any{}
	}
	if err := json.Unmarshal(raw.Data, target); err != nil {
		return err
	}
	switch value := target.(type) {
	case *FamilyRoutingData:
		b.Data = *value
	case *SocialEnvironmentData:
		b.Data = *value
	case *ViewClimateData:
		b.Data = *value
	case *map[string]any:
		b.Data = *value
	}
	return nil
}

type VerdictInfo struct {
	Headline      string  `json:"headline"`
	Confidence    float64 `json:"confidence"`
	LayersChecked int     `json:"layers_checked"`
}

type BriefItem struct {
	Label  string      `json:"label"`
	Status BriefStatus `json:"status"`
}

type CompromiseNote struct {
	BlockKey string `json:"block_key"`
	Text     string `json:"text"`
}

type RelaxationNote struct {
	Text string `json:"text"`
}

type LineStringGeometry struct {
	Type        string      `json:"type"`
	Coordinates [][]float64 `json:"coordinates"`
}

type FamilyRouteLeg struct {
	ToLabel  string             `json:"to_label"`
	ToKind   DestinationKind    `json:"to_kind"`
	Mode     TravelMode         `json:"mode"`
	Depart   string             `json:"depart"`
	Arrive   string             `json:"arrive"`
	Minutes  int                `json:"minutes"`
	Safety   LegSafety          `json:"safety"`
	Geometry LineStringGeometry `json:"geometry"`
}

type FamilyMember struct {
	ID    string           `json:"id"`
	Label string           `json:"label"`
	Legs  []FamilyRouteLeg `json:"legs"`
}

type FamilyRoutingData struct {
	Home    []float64      `json:"home"`
	Members []FamilyMember `json:"members"`
}

type SocialEnvironmentData struct {
	Home    []float64          `json:"home,omitempty"`
	RadiusM int                `json:"radius_m"`
	Scores  map[string]float64 `json:"scores"`
	Heat    map[string]any     `json:"heat"`
	POIs    []map[string]any   `json:"pois,omitempty"`
}

type ViewClimateData struct {
	OrientationDeg   float64            `json:"orientation_deg"`
	DirectLight      map[string]string  `json:"direct_light"`
	SunHoursBySeason map[string]float64 `json:"sun_hours_by_season"`
	CloudinessFactor float64            `json:"cloudiness_factor"`
	Obstructions     []map[string]any   `json:"obstructions"`
	ViewType         ViewType           `json:"view_type"`
	DB               float64            `json:"db"`
}

type DossierPayload struct {
	Verdict       VerdictInfo      `json:"verdict"`
	Brief         []BriefItem      `json:"brief"`
	Blocks        []Block          `json:"blocks"`
	Compromises   []CompromiseNote `json:"compromises"`
	Relaxation    []RelaxationNote `json:"relaxation"`
	ZoneRationale string           `json:"zone_rationale"`
}

type LifestyleAnalysis struct {
	MatchScore    int              `json:"match_score"`
	Summary       string           `json:"summary"`
	Verdict       VerdictInfo      `json:"verdict"`
	Brief         []BriefItem      `json:"brief"`
	Blocks        []Block          `json:"blocks"`
	Compromises   []CompromiseNote `json:"compromises"`
	Relaxation    []RelaxationNote `json:"relaxation"`
	ZoneRationale string           `json:"zone_rationale"`
}

type ObjectPassport struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Address           string            `json:"address"`
	Price             *int64            `json:"price"`
	Rooms             *int              `json:"rooms"`
	AreaSqm           *float64          `json:"area_sqm"`
	Floor             string            `json:"floor"`
	Images            []string          `json:"images"`
	Coordinates       []float64         `json:"coordinates"`
	LifestyleAnalysis LifestyleAnalysis `json:"lifestyle_analysis"`
}

// dossierStore и listingSource — части ChatSearchRepo и ListingRepo, нужные
// этому сервису. Обособленные интерфейсы — тот же приём, что у chatSearchStore
// в search_stream_service.go и resultsLister в results_service.go: позволяют
// проверить деградацию (ML упала, кэш протух) без реальной БД.
type dossierStore interface {
	GetResult(ctx context.Context, chatID uuid.UUID, externalID string) (domain.ChatSearchResult, error)
	GetSearch(ctx context.Context, id uuid.UUID) (domain.ChatSearch, error)
	SaveDossier(ctx context.Context, chatID, searchID uuid.UUID,
		externalID, version string, dossier map[string]any) error
}

type listingSource interface {
	GetByExternalID(ctx context.Context, externalID string) (domain.Listing, error)
	GetUpdatedAt(ctx context.Context, externalID string) (*time.Time, error)
}

type ObjectService struct {
	chats     *ChatService
	results   dossierStore
	listings  listingSource
	ml        *client.MLClient
	mlTimeout time.Duration
	// ttlHours — срок жизни кэша chat_search_results.dossier (Task 7, config
	// DossierTTLHours). Кэш старше этого числа часов считается протухшим.
	ttlHours int

	mu       sync.Mutex
	inFlight map[string]*dossierCall
}

type dossierCall struct {
	done    chan struct{}
	payload DossierPayload
}

func NewObjectService(chats *ChatService, results *repository.ChatSearchRepo,
	listings *repository.ListingRepo, ml *client.MLClient, mlTimeout time.Duration,
	ttlHours int) *ObjectService {
	return &ObjectService{chats: chats, results: results, listings: listings,
		ml: ml, mlTimeout: mlTimeout, ttlHours: ttlHours, inFlight: make(map[string]*dossierCall)}
}

func (s *ObjectService) GetPassport(ctx context.Context, userID, chatID uuid.UUID, objectID string) (ObjectPassport, error) {
	// chatID == uuid.Nil — объект открыт с карты, вне подбора: контекста запроса
	// нет, поэтому ни процента совпадения, ни досье не будет (см.
	// buildStandalonePassport). Так карта может открыть ЛЮБОЕ объявление, а не
	// только попавшее в выдачу.
	if chatID == uuid.Nil {
		listing, err := s.listings.GetByExternalID(ctx, objectID)
		if err != nil {
			return ObjectPassport{}, apperr.ObjectNotFound()
		}
		return buildStandalonePassport(listing), nil
	}

	chat, err := s.chats.GetOwned(ctx, userID, chatID)
	if err != nil {
		return ObjectPassport{}, err
	}

	res, err := s.results.GetResult(ctx, chatID, objectID)
	if errors.Is(err, repository.ErrNotFound) {
		// Объект есть в базе, но в этом чате не искался — отдаём его как с карты,
		// а не 404: пользователь мог прийти по прямой ссылке.
		listing, lerr := s.listings.GetByExternalID(ctx, objectID)
		if lerr != nil {
			return ObjectPassport{}, apperr.ObjectNotFound()
		}
		return buildStandalonePassport(listing), nil
	}
	if err != nil {
		return ObjectPassport{}, err
	}

	listing, err := s.listings.GetByExternalID(ctx, objectID)
	if err != nil {
		// Defensive only: chat_search_results.external_id only ever comes from
		// a real listings row written moments earlier during a search stream.
		return ObjectPassport{}, apperr.ObjectNotFound()
	}

	analysis := fallbackAnalysis(res.MatchScore, res.Explanation, res.AddressFacts)
	if chat.City == "msk" && s.ml != nil {
		if dossier, ok := s.dossier(ctx, chatID, objectID, chat.City, res); ok {
			analysis.Verdict = dossier.Verdict
			analysis.Brief = nonNilBrief(dossier.Brief)
			analysis.Blocks = nonNilBlocks(dossier.Blocks)
			analysis.Compromises = nonNilCompromises(dossier.Compromises)
			analysis.Relaxation = nonNilRelaxation(dossier.Relaxation)
			analysis.ZoneRationale = dossier.ZoneRationale
		}
	}

	p := staticPassport(listing)
	p.LifestyleAnalysis = analysis
	return p, nil
}

// staticPassport — часть паспорта, которая не зависит от запроса: всё берётся
// из строки listings.
func staticPassport(l domain.Listing) ObjectPassport {
	address := ""
	if l.Address != nil {
		address = *l.Address
	}
	images := l.Photos
	if len(images) == 0 {
		images = []string{PlaceholderCoverImage}
	}
	var coords []float64
	if l.Lon != nil && l.Lat != nil {
		coords = []float64{*l.Lon, *l.Lat}
	}
	return ObjectPassport{
		ID:          l.ExternalID,
		Name:        SynthName(l.Rooms, l.Area),
		Address:     address,
		Price:       l.Price,
		Rooms:       l.Rooms,
		AreaSqm:     l.Area,
		Floor:       FormatFloor(l.Level, l.Levels),
		Images:      images,
		Coordinates: coords,
	}
}

// buildStandalonePassport — объект, открытый с карты, вне подбора.
//
// Ни процента совпадения, ни досье здесь быть не может: оба привязаны к запросу
// (match_score считается по скору выдачи, досье строится из raw_query/
// parsed_query). Выдумывать их для объекта без запроса — ровно то, что проект
// запрещает. Остаются факты объекта: статика и блоки, собранные из его же
// address_facts.
func buildStandalonePassport(l domain.Listing) ObjectPassport {
	p := staticPassport(l)
	p.LifestyleAnalysis = LifestyleAnalysis{
		MatchScore: 0,
		Summary:    "",
		Verdict: VerdictInfo{Headline: "Объект открыт с карты, вне подбора",
			Confidence: 0, LayersChecked: 0},
		Brief:       []BriefItem{},
		Blocks:      buildBlocks(listingFacts(l)),
		Compromises: []CompromiseNote{},
		Relaxation:  []RelaxationNote{},
	}
	return p
}

// listingFacts — факты объекта в той же форме, что приходят из ML в
// address_facts, чтобы buildBlocks работал одинаково в обоих режимах.
func listingFacts(l domain.Listing) map[string]any {
	facts := map[string]any{}
	if l.MetroStation != nil && *l.MetroStation != "" {
		facts["metro_station"] = *l.MetroStation
	}
	if l.Address != nil && *l.Address != "" {
		facts["address"] = *l.Address
	}
	if l.WalkMinSchool != nil {
		facts["walk_min_school"] = *l.WalkMinSchool
	}
	if l.WalkMinMetro != nil {
		facts["walk_min_metro"] = *l.WalkMinMetro
	}
	if l.WalkMinPark != nil {
		facts["walk_min_park"] = *l.WalkMinPark
	}
	if l.BarDensity500m != nil {
		facts["bar_density_500m"] = float64(*l.BarDensity500m)
	}
	if l.NoiseLevel != nil && *l.NoiseLevel != "" {
		facts["noise_level"] = *l.NoiseLevel
	}
	return facts
}

func fallbackAnalysis(matchScore int, summary string, facts map[string]any) LifestyleAnalysis {
	return LifestyleAnalysis{
		MatchScore: matchScore, Summary: summary,
		Verdict: VerdictInfo{Headline: "Недостаточно данных для уверенного вердикта",
			Confidence: 0, LayersChecked: 0},
		Brief: []BriefItem{}, Blocks: buildBlocks(facts),
		Compromises: []CompromiseNote{}, Relaxation: []RelaxationNote{},
		ZoneRationale: "",
	}
}

func decodeDossier(raw map[string]any) (DossierPayload, bool) {
	b, err := json.Marshal(raw)
	if err != nil {
		return DossierPayload{}, false
	}
	var dossier DossierPayload
	if err := json.Unmarshal(b, &dossier); err != nil {
		return DossierPayload{}, false
	}
	return dossier, true
}

// dossierFresh — кэш досье годен, пока не истёк TTL И объект не обновлялся в
// listings после того, как досье было посчитано (Task 7). Любое из двух
// условий — повод перезапросить ML, а не молча отдать прошлогодний кэш:
// данные объекта обновляются циклом сбора независимо от того, когда в чате
// последний раз открывали его паспорт.
func dossierFresh(dossierUpdatedAt, listingUpdatedAt *time.Time, ttlHours int, now time.Time) bool {
	if dossierUpdatedAt == nil {
		return false
	}
	if now.Sub(*dossierUpdatedAt) > time.Duration(ttlHours)*time.Hour {
		return false
	}
	if listingUpdatedAt != nil && listingUpdatedAt.After(*dossierUpdatedAt) {
		return false
	}
	return true
}

func (s *ObjectService) dossier(ctx context.Context, chatID uuid.UUID, objectID, city string,
	res domain.ChatSearchResult) (DossierPayload, bool) {
	// Протухший кэш держим наготове: если ML не ответит, отдать досье суточной
	// давности честнее, чем не отдать ничего. Обмен «слегка устаревшее» на
	// «блока нет» был бы ухудшением деградации, а не улучшением свежести.
	var stale DossierPayload
	staleOK := false
	if res.DossierVersion == DossierSchemaVersion && res.Dossier != nil {
		stale, staleOK = decodeDossier(res.Dossier)
		listingUpdatedAt, err := s.listings.GetUpdatedAt(ctx, objectID)
		if err != nil {
			// Не удалось сверить свежесть с listings — не считаем это поводом
			// бить в ML: решаем по одному TTL, деградация мягкая.
			listingUpdatedAt = nil
		}
		if dossierFresh(res.DossierUpdatedAt, listingUpdatedAt, s.ttlHours, time.Now()) {
			return stale, staleOK
		}
	}
	key := chatID.String() + "\x00" + objectID
	s.mu.Lock()
	if call, exists := s.inFlight[key]; exists {
		s.mu.Unlock()
		select {
		case <-call.done:
			if call.payload.Verdict.Headline != "" {
				return call.payload, true
			}
			return stale, staleOK // чужой вызов не дал досье — свой кэш всё ещё лучше пустоты
		case <-ctx.Done():
			return stale, staleOK
		}
	}
	call := &dossierCall{done: make(chan struct{})}
	s.inFlight[key] = call
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inFlight, key)
		close(call.done)
		s.mu.Unlock()
	}()

	search, err := s.results.GetSearch(ctx, res.SearchID)
	if err != nil {
		return stale, staleOK
	}
	mlCtx, cancel := context.WithTimeout(ctx, s.mlTimeout)
	defer cancel()
	callStart := time.Now()
	response, err := s.ml.Dossier(mlCtx, client.DossierRequest{
		ObjectID: objectID, City: city, RawQuery: search.RawQuery,
		ParsedQuery: search.ParsedQuery, Relaxed: nonNilStrings(search.Relaxed),
		Degraded: nonNilStrings(search.Degraded),
	})
	observability.Default.ObserveMLCall("dossier", time.Since(callStart).Seconds())
	if err != nil || response.SchemaVersion != DossierSchemaVersion {
		return stale, staleOK // ML недоступна — лучше устаревшее, чем пусто
	}
	payload, ok := decodeDossier(response.Dossier)
	if !ok {
		return stale, staleOK
	}
	call.payload = payload
	_ = s.results.SaveDossier(ctx, chatID, res.SearchID, objectID,
		response.SchemaVersion, response.Dossier)
	return payload, true
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nonNilBrief(values []BriefItem) []BriefItem {
	if values == nil {
		return []BriefItem{}
	}
	return values
}
func nonNilBlocks(values []Block) []Block {
	if values == nil {
		return []Block{}
	}
	return values
}
func nonNilCompromises(values []CompromiseNote) []CompromiseNote {
	if values == nil {
		return []CompromiseNote{}
	}
	return values
}
func nonNilRelaxation(values []RelaxationNote) []RelaxationNote {
	if values == nil {
		return []RelaxationNote{}
	}
	return values
}

func buildBlocks(facts map[string]any) []Block {
	blocks := []Block{}

	if hasAny(facts, "walk_min_school", "walk_min_metro") {
		blocks = append(blocks, Block{
			Key: "logistics", Tier: "secondary", Title: "Логистика и школы", Icon: "school",
			Score:       walkScore(facts),
			Description: logisticsDescription(facts),
		})
	}
	if hasAny(facts, "bar_density_500m") {
		blocks = append(blocks, Block{
			Key: "social_environment", Tier: "secondary", Title: "Окружение", Icon: "users",
			Score:       barScore(facts),
			Description: socialDescription(facts),
		})
	}
	if hasAny(facts, "window_orientation", "noise_level") {
		blocks = append(blocks, Block{
			Key: "view_and_climate", Tier: "secondary", Title: "Вид и климат", Icon: "sun",
			Score:       noiseScore(facts),
			Description: climateDescription(facts),
		})
	}
	return blocks
}

func hasAny(facts map[string]any, keys ...string) bool {
	for _, k := range keys {
		if v, ok := facts[k]; ok && v != nil {
			return true
		}
	}
	return false
}

func walkScore(facts map[string]any) Grade {
	v, ok := numFact(facts, "walk_min_school")
	if !ok {
		v, ok = numFact(facts, "walk_min_metro")
	}
	if !ok {
		return "B"
	}
	switch {
	case v <= 10:
		return "A"
	case v <= 15:
		return "B+"
	case v <= 20:
		return "B"
	default:
		return "C"
	}
}

func logisticsDescription(facts map[string]any) string {
	if v, ok := numFact(facts, "walk_min_school"); ok {
		return formatMinutes(v) + " до школы пешком."
	}
	if v, ok := numFact(facts, "walk_min_metro"); ok {
		return formatMinutes(v) + " до метро пешком."
	}
	return ""
}

// barScore reuses the exact ">2 bars within 200m" threshold that
// habitus/geo/enrich.py already uses for noise_level, rather than inventing a
// new cutoff — see plan §5.
func barScore(facts map[string]any) Grade {
	v, ok := numFact(facts, "bar_density_500m")
	if !ok {
		return "B"
	}
	switch {
	case v == 0:
		return "A"
	case v <= 2:
		return "B"
	default:
		return "C"
	}
}

func socialDescription(facts map[string]any) string {
	if v, ok := numFact(facts, "bar_density_500m"); ok {
		return formatCount(v) + " баров/алкомаркетов в радиусе 500 м."
	}
	return ""
}

func noiseScore(facts map[string]any) Grade {
	if lvl, ok := facts["noise_level"].(string); ok {
		switch lvl {
		case "low":
			return "A-"
		case "medium":
			return "B"
		case "high":
			return "C"
		}
	}
	return "B"
}

func climateDescription(facts map[string]any) string {
	var parts []string
	if ors, ok := facts["window_orientation"]; ok {
		if list, ok := ors.([]any); ok && len(list) > 0 {
			parts = append(parts, "Окна ориентированы на разные стороны света.")
			_ = list
		}
	}
	if lvl, ok := facts["noise_level"].(string); ok && lvl != "" {
		parts = append(parts, "Уровень шума: "+lvl+".")
	}
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += " " + p
	}
	return out
}

func formatMinutes(v float64) string {
	return trimFloat(v) + " минут"
}

func formatCount(v float64) string {
	return trimFloat(v)
}

func trimFloat(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}
