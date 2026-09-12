package plugin

import (
	"strings"
	"testing"
)

func TestDecodeManifest(t *testing.T) {
	manifest, err := DecodeManifest(strings.NewReader(`{
		"name": "item-images",
		"version": "0.1.0",
		"apiVersion": "1",
		"entry": "bin/item-images",
		"hooks": ["item.created"]
	}`))
	if err != nil {
		t.Fatalf("DecodeManifest returned error: %v", err)
	}
	if manifest.Name != "item-images" {
		t.Fatalf("Name = %q, want item-images", manifest.Name)
	}
	if len(manifest.Hooks) != 1 || manifest.Hooks[0] != "item.created" {
		t.Fatalf("Hooks = %#v, want item.created", manifest.Hooks)
	}
}

func TestValidateManifestRejectsMissingName(t *testing.T) {
	err := (Manifest{Version: "0.1.0", APIVersion: APIVersion, Entry: "bin/plugin"}).Validate()
	if err == nil {
		t.Fatal("Validate returned nil, want error")
	}
}

func TestValidateManifestRejectsUnsupportedAPIVersion(t *testing.T) {
	err := (Manifest{Name: "plugin", Version: "0.1.0", APIVersion: "2", Entry: "bin/plugin"}).Validate()
	if err == nil {
		t.Fatal("Validate returned nil, want error")
	}
}
