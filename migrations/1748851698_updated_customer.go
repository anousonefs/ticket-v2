package migrations

import (
	"encoding/json"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		dao := app.Dao()

		collection, err := dao.FindCollectionByNameOrId("m0j53m407dmt3bq")
		if err != nil {
			return err
		}

		// add
		new_first_name := &core.SchemaField{}
		json.Unmarshal([]byte(`{
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
		}`), new_first_name)
		collection.Schema.AddField(new_first_name)

		// add
		new_last_name := &core.SchemaField{}
		json.Unmarshal([]byte(`{
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
		}`), new_last_name)
		collection.Schema.AddField(new_last_name)

		// add
		new_gender := &core.SchemaField{}
		json.Unmarshal([]byte(`{
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
		}`), new_gender)
		collection.Schema.AddField(new_gender)

		return dao.SaveCollection(collection)
	}, func(app core.App) error {
		dao := app.Dao()

		collection, err := dao.FindCollectionByNameOrId("m0j53m407dmt3bq")
		if err != nil {
			return err
		}

		// remove
		collection.Schema.RemoveField("yqensgpu")

		// remove
		collection.Schema.RemoveField("9cvdtuy4")

		// remove
		collection.Schema.RemoveField("nxyssebn")

		return dao.SaveCollection(collection)
	})
}
