package config

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	validator "github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

func compiledSchemas(t *testing.T) map[string]*validator.Schema {
	t.Helper()
	generated, err := Schemas()
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]*validator.Schema)
	for name, data := range generated {
		committed, err := os.ReadFile(filepath.Join("..", "..", "schemas", name))
		if err != nil {
			t.Fatal(err)
		}
		var actual, expected bytes.Buffer
		if err := json.Compact(&actual, committed); err != nil {
			t.Fatal(err)
		}
		if err := json.Compact(&expected, data); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual.Bytes(), expected.Bytes()) {
			t.Fatalf("%s is stale; run lgmon config schema", name)
		}
		var document any
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		compiler := validator.NewCompiler()
		uri := "https://example.test/" + name
		if err := compiler.AddResource(uri, document); err != nil {
			t.Fatal(err)
		}
		result[name], err = compiler.Compile(uri)
		if err != nil {
			t.Fatalf("invalid generated schema %s: %v", name, err)
		}
	}
	return result
}

func TestExamplesMatchGeneratedSchemas(t *testing.T) {
	schemas := compiledSchemas(t)
	examples := os.DirFS(filepath.Join("..", "..", "examples"))
	err := fs.WalkDir(examples, ".", func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || path.Ext(file) != ".yaml" {
			return nil
		}
		data, err := fs.ReadFile(examples, file)
		if err != nil {
			return err
		}
		firstLine := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
		const prefix = "# yaml-language-server: $schema="
		if !strings.HasPrefix(firstLine, prefix) {
			t.Fatalf("%s is missing its schema declaration", file)
		}
		name := path.Base(strings.TrimPrefix(firstLine, prefix))
		if schemas[name] == nil {
			t.Fatalf("%s references an unknown schema %s", file, name)
		}
		var decoded any
		if err := yaml.Unmarshal(data, &decoded); err != nil {
			return err
		}
		serialized, err := json.Marshal(decoded)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(serialized, &decoded); err != nil {
			return err
		}
		if err := schemas[name].Validate(decoded); err != nil {
			t.Fatalf("%s does not match %s: %v", file, name, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSchemasRejectInvalidShapes(t *testing.T) {
	schemas := compiledSchemas(t)
	cases := []struct{ name, schema, data string }{
		{"empty match", "targets.schema.json", `{"targets":[{"id":"web","match":{}}]}`},
		{"empty process", "targets.schema.json", `{"targets":[{"id":"web","match":{"process":""}}]}`},
		{"wrong ID", "targets.schema.json", `{"targets":[{"id":"Upper_ID","match":{"process":"demo.exe"}}]}`},
		{"bad port", "targets.schema.json", `{"targets":[{"id":"web","match":{"port":65536}}]}`},
		{"missing TCP address", "targets.schema.json", `{"targets":[{"id":"web","match":{"service":"demo"},"probes":[{"type":"tcp","interval":"1s","timeout":"1s"}]}]}`},
		{"unknown field", "rules.schema.json", `{"rules":[],"unknown":true}`},
		{"offline target", "rules.schema.json", `{"rules":[{"id":"offline","type":"collector_offline","target":"web","severity":"warning","notify":["alerts"]}]}`},
		{"normal rule without target", "rules.schema.json", `{"rules":[{"id":"down","type":"process_absent","severity":"warning","notify":["alerts"]}]}`},
		{"two template modes", "card-template.schema.json", `{"id":"demo","extends":"standard","raw":{}}`},
		{"no template mode", "card-template.schema.json", `{"id":"demo"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var document any
			if err := json.Unmarshal([]byte(tc.data), &document); err != nil {
				t.Fatal(err)
			}
			if err := schemas[tc.schema].Validate(document); err == nil {
				t.Fatal("schema accepted an invalid configuration")
			}
		})
	}
}
