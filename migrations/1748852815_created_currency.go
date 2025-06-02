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
			"id": "1fdzssd6pn9lpov",
			"created": "2025-06-02 08:26:55.845Z",
			"updated": "2025-06-02 08:26:55.845Z",
			"name": "currency",
			"type": "base",
			"system": false,
			"schema": [
				{
					"system": false,
					"id": "btedb1gm",
					"name": "ccy",
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
					"id": "ddziyqa8",
					"name": "type",
					"type": "select",
					"required": false,
					"presentable": false,
					"unique": false,
					"options": {
						"maxSelect": 1,
						"values": [
							"SELL",
							"BUY"
						]
					}
				},
				{
					"system": false,
					"id": "egi61w57",
					"name": "rate",
					"type": "number",
					"required": false,
					"presentable": false,
					"unique": false,
					"options": {
						"min": null,
						"max": null,
						"noDecimal": false
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

		collection, err := dao.FindCollectionByNameOrId("1fdzssd6pn9lpov")
		if err != nil {
			return err
		}

		return dao.DeleteCollection(collection)
	})
}
