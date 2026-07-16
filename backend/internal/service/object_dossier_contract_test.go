package service

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"habitus-backend/internal/client"
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
	analysis := fallbackAnalysis(.9, "summary", map[string]any{})
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
