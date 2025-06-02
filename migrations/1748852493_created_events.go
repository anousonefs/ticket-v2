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
			"id": "q5x1b6whizo4zzo",
			"created": "2025-06-02 08:21:33.123Z",
			"updated": "2025-06-02 08:21:33.123Z",
			"name": "events",
			"type": "base",
			"system": false,
			"schema": [
				{
					"system": false,
					"id": "aw29zuqt",
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
					"id": "nmacsz2a",
					"name": "start_at",
					"type": "date",
					"required": false,
					"presentable": false,
					"unique": false,
					"options": {
						"min": "",
						"max": ""
					}
				},
				{
					"system": false,
					"id": "jpgzftma",
					"name": "end_at",
					"type": "date",
					"required": false,
					"presentable": false,
					"unique": false,
					"options": {
						"min": "",
						"max": ""
					}
				},
				{
					"system": false,
					"id": "453lmg86",
					"name": "entance_at",
					"type": "date",
					"required": false,
					"presentable": false,
					"unique": false,
					"options": {
						"min": "",
						"max": ""
					}
				},
				{
					"system": false,
					"id": "hyoqsdro",
					"name": "status",
					"type": "select",
					"required": false,
					"presentable": false,
					"unique": false,
					"options": {
						"maxSelect": 1,
						"values": [
							"publish",
							"unpublish"
						]
					}
				},
				{
					"system": false,
					"id": "uekfysog",
					"name": "description",
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
					"id": "flpv6elv",
					"name": "location",
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
					"id": "am1ugjr8",
					"name": "image_url",
					"type": "url",
					"required": false,
					"presentable": false,
					"unique": false,
					"options": {
						"exceptDomains": null,
						"onlyDomains": null
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

		collection, err := dao.FindCollectionByNameOrId("q5x1b6whizo4zzo")
		if err != nil {
			return err
		}

		return dao.DeleteCollection(collection)
	})
}
