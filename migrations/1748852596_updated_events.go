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

		collection, err := dao.FindCollectionByNameOrId("q5x1b6whizo4zzo")
		if err != nil {
			return err
		}

		// remove
		collection.Schema.RemoveField("am1ugjr8")

		// add
		new_image_url := &core.SchemaField{}
		json.Unmarshal([]byte(`{
			"system": false,
			"id": "v8viprpw",
			"name": "image_url",
			"type": "file",
			"required": false,
			"presentable": false,
			"unique": false,
			"options": {
				"mimeTypes": [],
				"thumbs": [],
				"maxSelect": 1,
				"maxSize": 5242880,
				"protected": false
			}
		}`), new_image_url)
		collection.Schema.AddField(new_image_url)

		return dao.SaveCollection(collection)
	}, func(db dbx.Builder) error {
		dao := core.NewBaseApp().Dao()

		collection, err := dao.FindCollectionByNameOrId("q5x1b6whizo4zzo")
		if err != nil {
			return err
		}

		// add
		del_image_url := &core.SchemaField{}
		json.Unmarshal([]byte(`{
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
		}`), del_image_url)
		collection.Schema.AddField(del_image_url)

		// remove
		collection.Schema.RemoveField("v8viprpw")

		return dao.SaveCollection(collection)
	})
}
