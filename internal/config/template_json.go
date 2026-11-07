package config

import "encoding/json"

func (t Template) MarshalJSON() ([]byte, error) {
	type definition Template
	var fields *[]string
	// Nil inherits the preset, while an explicit empty list hides all built-in fields.
	if t.Fields != nil {
		fields = &t.Fields
	}
	return json.Marshal(struct {
		definition
		Fields *[]string `json:"fields,omitempty"`
	}{definition: definition(t), Fields: fields})
}
