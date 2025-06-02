package migrations

import (
	"encoding/json"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(db dbx.Builder) error {
		dao := core.NewBaseApp().Dao()

		collection, err := dao.FindCollectionByNameOrId("m0j53m407dmt3bq")
		if err != nil {
			return err
		}

		return dao.DeleteCollection(collection)
	}, func(db dbx.Builder) error {
		jsonData := `{
			"id": "m0j53m407dmt3bq",
			"created": "2025-06-02 08:04:54.630Z",
			"updated": "2025-06-02 08:08:18.237Z",
			"name": "customer",
			"type": "auth",
			"system": false,
			"schema": [
				{
					"system": false,
					"id": "yqensgpu",
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
					"id": "9cvdtuy4",
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
					"id": "nxyssebn",
					"name": "gender",
					"type": "text",
					"required": false,
					"presentable": false,
					"unique": false,
					"options": {
						"min": null,
						"max": null,
						"pattern": ""
					}
				}
			],
			"indexes": [],
			"listRule": null,
			"viewRule": null,
			"createRule": null,
			"updateRule": null,
			"deleteRule": null,
			"options": {
				"allowEmailAuth": true,
				"allowOAuth2Auth": true,
				"allowUsernameAuth": true,
				"exceptEmailDomains": null,
				"manageRule": null,
				"minPasswordLength": 8,
				"onlyEmailDomains": null,
				"onlyVerified": false,
				"requireEmail": false
			}
		}`

		collection := &core.Collection{}
		if err := json.Unmarshal([]byte(jsonData), &collection); err != nil {
			return err
		}

		return core.NewBaseApp().Dao().SaveCollection(collection)
	})
}
