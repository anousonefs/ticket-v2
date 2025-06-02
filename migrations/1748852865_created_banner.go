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
			"id": "zdkx7cme58kkfww",
			"created": "2025-06-02 08:27:45.600Z",
			"updated": "2025-06-02 08:27:45.600Z",
			"name": "banner",
			"type": "base",
			"system": false,
			"schema": [
				{
					"system": false,
					"id": "by7ttsui",
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
					"id": "uuv2l9np",
					"name": "img_url",
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

		collection, err := dao.FindCollectionByNameOrId("zdkx7cme58kkfww")
		if err != nil {
			return err
		}

		return dao.DeleteCollection(collection)
	})
}
