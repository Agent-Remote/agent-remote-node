package skillmanager

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSharedManifestVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/manifest-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Valid []struct {
			Name     string `json:"name"`
			Manifest string `json:"manifest_json"`
			Digest   string `json:"tree_sha256"`
		} `json:"valid"`
		Invalid []struct {
			Name     string `json:"name"`
			Manifest string `json:"manifest_json"`
		} `json:"invalid"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, item := range vectors.Valid {
		t.Run(item.Name, func(t *testing.T) {
			manifest, err := DecodeManifest([]byte(item.Manifest))
			if err != nil {
				t.Fatal(err)
			}
			digest, err := Digest(manifest)
			if err != nil || digest != item.Digest {
				t.Fatalf("digest = %q, error = %v; want %q", digest, err, item.Digest)
			}
		})
	}
	for _, item := range vectors.Invalid {
		t.Run(item.Name, func(t *testing.T) {
			if _, err := DecodeManifest([]byte(item.Manifest)); err == nil {
				t.Fatal("invalid manifest was accepted")
			}
		})
	}
}

func TestManifestRejectsNoncanonicalJSONTypes(t *testing.T) {
	for _, input := range []string{
		`null`,
		`{"Version":1,"entries":[]}`,
		`{"version":1.0,"entries":[]}`,
		`{"version":1,"entries":[{"path":null,"kind":"directory"}]}`,
		`{"version":1,"entries":[{"path":"a","kind":"directory","mode":"493"}]}`,
		`{"version":1,"entries":[{"path":"\ud800","kind":"directory"}]}`,
	} {
		if _, err := DecodeManifest([]byte(input)); err == nil {
			t.Errorf("accepted invalid manifest: %s", input)
		}
	}
}
