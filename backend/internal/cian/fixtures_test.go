package cian

// offerResponseJSON — усечённый до нужных полей реальный ответ
// api.cian.ru/search-offers/v2/search-offers-desktop/ на запрос с фильтром ids.
// Живого Циана в тестах нет: он отдаёт 403 из CI и капчу под нагрузкой.
// Пути полей — те, что читает parseOffer, включая bargainTerms.price.
const offerResponseJSON = `{
  "data": {
    "offersSerialized": [
      {
        "id": 318394906,
        "bargainTerms": {"price": 45007350},
        "totalArea": "46.5",
        "roomsCount": 1,
        "floorNumber": 2,
        "building": {"floorsCount": 18, "materialType": "monolith"},
        "geo": {
          "userInput": "Москва, 2-й Донской проезд",
          "coordinates": {"lat": 55.71120458532715, "lng": 37.592330829357934},
          "undergrounds": [
            {"name": "Ленинский проспект", "time": 7, "transportType": "walk"}
          ]
        },
        "photos": [{"fullUrl": "https://images.cdn-cian.ru/images/2943545902-1.jpg"}],
        "description": "1-к квартира в премиальном комплексе SHIFT",
        "fullUrl": "https://www.cian.ru/sale/flat/318394906/"
      }
    ]
  }
}`
