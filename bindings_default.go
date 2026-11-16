package main

import (
	"embed"

	"ofam.dev/ochazuke"
)

// bindings_picker.json holds more hidden key rows as data, loaded into the
// default layer right after bindings.json, so a row there never replaces one
// of those.

//go:embed bindings_picker.json
var pickerBindingsFS embed.FS

func loadPickerBindings(r *ochazuke.Runtime) error {
	return r.LoadDefaultBindingsFS(pickerBindingsFS, "bindings_picker.json")
}
