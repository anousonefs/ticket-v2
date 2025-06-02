package migrations

import (
	"encoding/json"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		jsonData := `{
			"id": "m0j53m407dmt3bq",
			"created": "2025-06-02 08:04:54.630Z",
			"updated": "2025-06-02 08:04:54.630Z",
			"name": "customer",
			"type": "auth",
			"system": false,
			"schema": [],
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

		dao := app.Dao()
		return dao.SaveCollection(collection)
	}, func(app core.App) error {
		dao := app.Dao()

		collection, err := dao.FindCollectionByNameOrId("m0j53m407dmt3bq")
		if err != nil {
			return err
		}

		return dao.DeleteCollection(collection)
	})
}
