package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"habitus-backend/internal/client"
	"habitus-backend/internal/domain"
)

func TestDecodeDossierNarrowsHeroDataByKey(t *testing.T) {
	var raw map[string]any
	_ = json.Unmarshal([]byte(`{
		"verdict":{"headline":"Подходит","confidence":0.9,"layers_checked":2},
		"brief":[],"compromises":[],"relaxation":[],"zone_rationale":"",
		"blocks":[{"key":"family_routing","tier":"hero","title":"Маршруты",
		"icon":"route","score":"A","description":"Проверено","data":{
		"home":[37.6,55.7],"members":[{"id":"son","label":"Сын","legs":[{
		"to_label":"Лицей","to_kind":"school","mode":"walk","depart":"08:15",
		"arrive":"08:26","minutes":11,"safety":"caution","geometry":{
		"type":"LineString","coordinates":[[37.6,55.7],[37.61,55.71]]}}]}]}}]}`), &raw)
	dossier, ok := decodeDossier(raw)
	if !ok || len(dossier.Blocks) != 1 {
		t.Fatalf("decodeDossier() = %#v, %v", dossier, ok)
	}
	data, ok := dossier.Blocks[0].Data.(FamilyRoutingData)
	if !ok || len(data.Members) != 1 || data.Members[0].Legs[0].Geometry.Coordinates[1][0] != 37.61 {
		t.Fatalf("typed family data = %#v", dossier.Blocks[0].Data)
	}
}

// Нога без времени и без замера безопасности должна доехать до фронта именно
// как null, а не как пустая строка: "" неотличимо от «полночь», и фронт
// нарисовал бы поездку в начале суток вместо честного «время не названо».
func TestDecodeDossierKeepsUntimedLegNull(t *testing.T) {
	var raw map[string]any
	_ = json.Unmarshal([]byte(`{
		"verdict":{"headline":"Подходит","confidence":0.9,"layers_checked":2},
		"brief":[],"compromises":[],"relaxation":[],"zone_rationale":"",
		"blocks":[{"key":"family_routing","tier":"hero","title":"Маршруты",
		"icon":"route","score":"B","description":"Оценка","data":{
		"home":[37.6,55.7],"members":[{"id":"son","label":"Сын","legs":[{
		"to_label":"Школа","to_kind":"school","mode":"walk","depart":null,
		"arrive":null,"minutes":13,"safety":null,"estimated":true,"geometry":{
		"type":"LineString","coordinates":[[37.6,55.7],[37.61,55.71]]}}]}]}}]}`), &raw)
	dossier, ok := decodeDossier(raw)
	if !ok {
		t.Fatal("decodeDossier() failed")
	}
	data := dossier.Blocks[0].Data.(FamilyRoutingData)
	leg := data.Members[0].Legs[0]
	if leg.Depart != nil || leg.Arrive != nil {
		t.Fatalf("время выдумано из null: depart=%v arrive=%v", leg.Depart, leg.Arrive)
	}
	if leg.Safety != nil {
		t.Fatalf("безопасность выдумана из null: %v", *leg.Safety)
	}
	if !leg.Estimated || leg.Minutes != 13 {
		t.Fatalf("оценка потеряна: estimated=%v minutes=%d", leg.Estimated, leg.Minutes)
	}

	// И обратно наружу: ключи должны быть именно null, а не "".
	out, err := json.Marshal(leg)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	_ = json.Unmarshal(out, &back)
	for _, key := range []string{"depart", "arrive", "safety"} {
		if back[key] != nil {
			t.Fatalf("%s ушёл наружу как %#v вместо null", key, back[key])
		}
	}
}

func TestParsedQueryPersistenceKeepsHouseholdTrips(t *testing.T) {
	depart := "08:15"
	parsed := client.ParsedQuery{Household: []client.HouseholdMemberIntent{{
		ID: "son", Label: "Сын", Legs: []client.HouseholdLegIntent{{
			ToLabel: "Лицей 239", ToKind: "school", Mode: "walk", Depart: &depart,
		}},
	}}}
	stored := parsedQueryToMap(parsed)
	household, ok := stored["household"].([]any)
	if !ok || len(household) != 1 {
		t.Fatalf("household lost during persistence: %#v", stored)
	}
	member := household[0].(map[string]any)
	legs := member["legs"].([]any)
	if legs[0].(map[string]any)["depart"] != depart {
		t.Fatalf("explicit trip time lost: %#v", legs[0])
	}
}

func TestFallbackAnalysisKeepsRequiredCollectionsPresent(t *testing.T) {
	analysis := fallbackAnalysis(90, "summary", map[string]any{})
	b, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	_ = json.Unmarshal(b, &payload)
	for _, key := range []string{"brief", "blocks", "compromises", "relaxation"} {
		if value, exists := payload[key]; !exists || value == nil {
			t.Fatalf("%s missing/null in %s", key, b)
		}
	}
	verdict := payload["verdict"].(map[string]any)
	if verdict["confidence"].(float64) != 0 || verdict["layers_checked"].(float64) != 0 {
		t.Fatalf("fallback verdict = %#v", verdict)
	}
}

func TestObjectAskLockIsScopedByObjectAndChat(t *testing.T) {
	service := NewObjectAskService(nil, nil, 0)
	chat := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	if !service.TryLock(chat, "one") || service.TryLock(chat, "one") {
		t.Fatal("same pair must conflict")
	}
	if !service.TryLock(chat, "two") {
		t.Fatal("different object must not conflict")
	}
	service.Unlock(chat, "one")
	if !service.TryLock(chat, "one") {
		t.Fatal("unlock must release pair")
	}
}

func TestPassportScoreMatchesListScore(t *testing.T) {
	// Список считает RescaleScore(score, rank, degraded); паспорт раньше
	// пересчитывал из stored-скора без ранга и degraded — числа расходились.
	stored := RescaleScore(0.031, 2, []string{"reranker"})
	analysis := fallbackAnalysis(stored, "", map[string]any{})
	if analysis.MatchScore != stored {
		t.Fatalf("паспорт показывает %d, список — %d", analysis.MatchScore, stored)
	}
}

func TestStandalonePassportHasNoInventedMatchScore(t *testing.T) {
	// Объект, открытый с карты, вне подбора: процента совпадения не существует —
	// показывать его нельзя, он привязан к запросу. Досье тоже строится из
	// raw_query/parsed_query, поэтому в этом режиме его нет.
	price := int64(21_300_000)
	rooms, area := 2, 40.0
	lon, lat := 37.6, 55.75
	school, metro := 6.0, 4.0
	l := domain.Listing{
		ExternalID: "cian_1", Price: &price, Rooms: &rooms, Area: &area,
		Lon: &lon, Lat: &lat, Address: strp("Москва, Снежная улица, 4"),
		Photos:        []string{"https://cdn/a.jpg", "https://cdn/b.jpg"},
		WalkMinSchool: &school, WalkMinMetro: &metro,
	}
	p := buildStandalonePassport(l)
	if p.LifestyleAnalysis.MatchScore != 0 {
		t.Fatalf("процент совпадения без запроса должен быть пустым, получено %d",
			p.LifestyleAnalysis.MatchScore)
	}
	if len(p.LifestyleAnalysis.Blocks) == 0 {
		t.Fatal("блоки из фактов объекта должны остаться")
	}
	if p.Address != "Москва, Снежная улица, 4" || len(p.Images) != 2 {
		t.Fatalf("статика объекта потерялась: %#v", p)
	}
	if p.Coordinates[0] != lon || p.Coordinates[1] != lat {
		t.Fatalf("координаты потерялись: %#v", p.Coordinates)
	}
	for _, c := range [][]string{{"brief", fmt.Sprint(p.LifestyleAnalysis.Brief)},
		{"compromises", fmt.Sprint(p.LifestyleAnalysis.Compromises)}} {
		if c[1] == "" {
			t.Fatalf("%s должен быть пустым срезом, а не nil", c[0])
		}
	}
}

func TestMetroRideSurvivesPassthrough(t *testing.T) {
	raw := []byte(`{"to_label":"офис","to_kind":"work","mode":"metro",
		"depart":"08:00","arrive":"08:25","minutes":25,"safety":"safe",
		"geometry":{"type":"LineString","coordinates":[[37.6,55.75],[37.62,55.76]]},
		"metro":{"walk_from_home_min":7,"walk_to_dest_min":5,"total_minutes":25,
			"wait_min":2,
			"estimated":false,
			"segments":[{"line_ref":"1","line_name":"Сокольническая",
				"system":"subway","colour":"#EF161E","from_station":"Сокольники",
				"to_station":"Охотный Ряд","stops":6,"minutes":8,"estimated":false}],
			"transfers":[{"from_station":"Охотный Ряд","to_station":"Театральная",
				"minutes":3,"outdoor":true,"estimated":false}]}}`)

	var leg FamilyRouteLeg
	if err := json.Unmarshal(raw, &leg); err != nil {
		t.Fatalf("нога не разобралась: %v", err)
	}
	if leg.Metro == nil {
		t.Fatal("разбивка поездки потеряна")
	}
	if leg.Metro.TotalMinutes != leg.Minutes {
		t.Fatalf("итог разошёлся с разбивкой: %d против %d",
			leg.Metro.TotalMinutes, leg.Minutes)
	}
	if leg.Metro.WaitMin != 2 {
		t.Fatalf("ожидание посадки потеряно: %d", leg.Metro.WaitMin)
	}
	if len(leg.Metro.Segments) != 1 || leg.Metro.Segments[0].System != SystemSubway {
		t.Fatalf("сегмент потерян: %#v", leg.Metro.Segments)
	}
	if leg.Metro.Segments[0].Colour == nil || *leg.Metro.Segments[0].Colour != "#EF161E" {
		t.Fatalf("цвет линии потерян: %#v", leg.Metro.Segments[0].Colour)
	}
	if !leg.Metro.Transfers[0].Outdoor {
		t.Fatal("признак уличной пересадки потерян")
	}

	back, err := json.Marshal(leg)
	if err != nil {
		t.Fatalf("обратная сериализация: %v", err)
	}
	if !bytes.Contains(back, []byte(`"outdoor":true`)) {
		t.Fatalf("признак не доехал наружу: %s", back)
	}
	if !bytes.Contains(back, []byte(`"wait_min":2`)) {
		t.Fatalf("ожидание посадки не доехало наружу: %s", back)
	}
}

// R69b: wait_min — обязательное поле-остаток округления, а не независимый
// замер интервала. Ноль в нём легитимен (части разбивки сошлись без
// остатка), и omitempty стёр бы этот ноль так же, как отсутствие поля —
// фронт не смог бы отличить «ожидания нет» от «поле потерялось».
func TestMetroRideWaitMinSurvivesWhenZero(t *testing.T) {
	raw := []byte(`{"to_label":"офис","to_kind":"work","mode":"metro",
		"depart":"08:00","arrive":"08:20","minutes":20,"safety":"safe",
		"geometry":{"type":"LineString","coordinates":[[37.6,55.75],[37.62,55.76]]},
		"metro":{"walk_from_home_min":7,"walk_to_dest_min":5,"total_minutes":20,
			"wait_min":0,"estimated":false,"segments":[],"transfers":[]}}`)

	var leg FamilyRouteLeg
	if err := json.Unmarshal(raw, &leg); err != nil {
		t.Fatalf("нога не разобралась: %v", err)
	}
	back, err := json.Marshal(leg)
	if err != nil {
		t.Fatalf("обратная сериализация: %v", err)
	}
	if !bytes.Contains(back, []byte(`"wait_min":0`)) {
		t.Fatalf("нулевое ожидание должно остаться в JSON явно: %s", back)
	}
}

// МЦК отдаёт цвет линии CSS-именем («red»), а не #rrggbb — colour остаётся
// нетипизированной строкой, hex нигде не предполагается. И для отсутствующего
// цвета поле остаётся явным null, а не пропадает и не превращается в "".
func TestMetroSegmentColourStaysNullableAndUnconstrained(t *testing.T) {
	raw := []byte(`{"to_label":"офис","to_kind":"work","mode":"metro",
		"depart":"08:00","arrive":"08:20","minutes":20,"safety":"safe",
		"geometry":{"type":"LineString","coordinates":[[37.6,55.75],[37.62,55.76]]},
		"metro":{"walk_from_home_min":7,"walk_to_dest_min":5,"total_minutes":20,
			"wait_min":1,"estimated":false,
			"segments":[
				{"line_ref":"14","line_name":"МЦК","system":"mck","colour":"red",
					"from_station":"Панфиловская","to_station":"Стрешнево",
					"stops":1,"minutes":4,"estimated":false},
				{"line_ref":"1","line_name":"Сокольническая","system":"subway",
					"colour":null,"from_station":"Сокольники","to_station":"Красносельская",
					"stops":1,"minutes":2,"estimated":true}],
			"transfers":[]}}`)

	var leg FamilyRouteLeg
	if err := json.Unmarshal(raw, &leg); err != nil {
		t.Fatalf("нога не разобралась: %v", err)
	}
	if leg.Metro.Segments[0].Colour == nil || *leg.Metro.Segments[0].Colour != "red" {
		t.Fatalf("CSS-имя цвета МЦК не должно предполагаться hex'ом: %#v",
			leg.Metro.Segments[0].Colour)
	}
	if leg.Metro.Segments[1].Colour != nil {
		t.Fatalf("отсутствующий цвет должен остаться nil: %#v",
			leg.Metro.Segments[1].Colour)
	}

	back, err := json.Marshal(leg)
	if err != nil {
		t.Fatalf("обратная сериализация: %v", err)
	}
	if !bytes.Contains(back, []byte(`"colour":"red"`)) {
		t.Fatalf("цвет МЦК не доехал наружу как есть: %s", back)
	}
	if !bytes.Contains(back, []byte(`"colour":null`)) {
		t.Fatalf("отсутствующий цвет должен уехать явным null, а не пропасть: %s", back)
	}
}

func TestNonMetroLegHasNoMetroField(t *testing.T) {
	raw := []byte(`{"to_label":"школа","to_kind":"school","mode":"walk",
		"depart":"08:00","arrive":"08:15","minutes":15,"safety":"safe",
		"geometry":{"type":"LineString","coordinates":[[37.6,55.75],[37.61,55.75]]}}`)
	var leg FamilyRouteLeg
	if err := json.Unmarshal(raw, &leg); err != nil {
		t.Fatalf("нога не разобралась: %v", err)
	}
	if leg.Metro != nil {
		t.Fatal("у пешей ноги не должно быть разбивки метро")
	}
	back, _ := json.Marshal(leg)
	if bytes.Contains(back, []byte(`"metro"`)) {
		t.Fatalf("пустое поле не должно уезжать наружу: %s", back)
	}
}

func TestDecodeDossierKeepsSourcesOnBlockWithoutData(t *testing.T) {
	// data:null — обычное состояние вторичного блока. У Block.UnmarshalJSON
	// на нём стоит ранний return, и присваивание Sources после него молча
	// теряло бы источники именно там, где они особенно нужны.
	var raw map[string]any
	_ = json.Unmarshal([]byte(`{
		"verdict":{"headline":"ok","confidence":0.5,"layers_checked":1},
		"brief":[],"compromises":[],"relaxation":[],"zone_rationale":"",
		"blocks":[{"key":"view_and_climate","title":"Вид и климат","score":"B",
		"description":"","data":null,"sources":[
		{"key":"noise","label":"Шум","kind":"proxy","basis":"модель по типам дорог",
		"observed_at":"2026-04-10"}]}]}`), &raw)
	dossier, ok := decodeDossier(raw)
	if !ok || len(dossier.Blocks) != 1 {
		t.Fatalf("decodeDossier() = %#v, %v", dossier, ok)
	}
	sources := dossier.Blocks[0].Sources
	if len(sources) != 1 || sources[0].Kind != "proxy" || sources[0].ObservedAt != "2026-04-10" {
		t.Fatalf("sources = %#v", sources)
	}
}
