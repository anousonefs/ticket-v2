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
			"id": "ghphhudot8is8qy",
			"created": "2025-06-02 08:16:12.254Z",
			"updated": "2025-06-02 08:16:12.254Z",
			"name": "customers",
			"type": "base",
			"system": false,
			"schema": [
				{
					"system": false,
					"id": "u2gdheg8",
					"name": "first_name",
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
					"id": "xojpxk4s",
					"name": "last_name",
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
					"id": "z3jn5xdu",
					"name": "gender",
					"type": "select",
					"required": false,
					"presentable": false,
					"unique": false,
					"options": {
						"maxSelect": 1,
						"values": [
							"male",
							"female"
						]
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

		collection, err := dao.FindCollectionByNameOrId("ghphhudot8is8qy")
		if err != nil {
			return err
		}

		return dao.DeleteCollection(collection)
	})
}
