package migrations

import (
	"encoding/json"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(db dbx.Builder) error {
		jsonData := `{
			"id": "bhlulsy1qhohb7u",
			"created": "2025-06-02 08:25:57.559Z",
			"updated": "2025-06-02 08:25:57.559Z",
			"name": "tickets",
			"type": "base",
			"system": false,
			"schema": [
				{
					"system": false,
					"id": "4bamkvnv",
					"name": "name",
					"type": "text",
					"required": false,
					"presentable": false,
					"unique": false,
					"options": {
						"min": null,
						"max": null,
						"pattern": ""
					}
				},
				{
					"system": false,
					"id": "ephtzwuf",
					"name": "count",
					"type": "number",
					"required": false,
					"presentable": false,
					"unique": false,
					"options": {
						"min": null,
						"max": null,
						"noDecimal": false
					}
				},
				{
					"system": false,
					"id": "7sl1p1ec",
					"name": "price",
					"type": "number",
					"required": false,
					"presentable": false,
					"unique": false,
					"options": {
						"min": null,
						"max": null,
						"noDecimal": false
					}
				},
				{
					"system": false,
					"id": "3r8v0won",
					"name": "status",
					"type": "select",
					"required": false,
					"presentable": false,
					"unique": false,
					"options": {
						"maxSelect": 1,
						"values": [
							"available",
							"soldout",
							"unavailable"
						]
					}
				},
				{
					"system": false,
					"id": "au65klou",
					"name": "type",
					"type": "select",
					"required": false,
					"presentable": false,
					"unique": false,
					"options": {
						"maxSelect": 1,
						"values": [
							"seat",
							"stand"
						]
					}
				},
				{
					"system": false,
					"id": "21erufd4",
					"name": "event_id",
					"type": "relation",
					"required": false,
					"presentable": false,
					"unique": false,
					"options": {
						"collectionId": "q5x1b6whizo4zzo",
						"cascadeDelete": false,
						"minSelect": null,
						"maxSelect": 1,
						"displayFields": null
					}
				}
			],
			"indexes": [],
			"listRule": null,
			"viewRule": null,
			"createRule": null,
			"updateRule": null,
			"deleteRule": null,
			"options": {}
		}`

		collection := &core.Collection{}
		if err := json.Unmarshal([]byte(jsonData), &collection); err != nil {
			return err
		}

		return core.NewBaseApp().Dao().SaveCollection(collection)
	}, func(db dbx.Builder) error {
		dao := core.NewBaseApp().Dao()

		collection, err := dao.FindCollectionByNameOrId("bhlulsy1qhohb7u")
		if err != nil {
			return err
		}

		return dao.DeleteCollection(collection)
	})
}
