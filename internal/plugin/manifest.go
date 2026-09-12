package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const APIVersion = "1"

type Manifest struct {
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	APIVersion  string          `json:"apiVersion"`
	Entry       string          `json:"entry"`
	Description string          `json:"description,omitempty"`
	Hooks       []string        `json:"hooks,omitempty"`
	Fields      []FieldManifest `json:"fields,omitempty"`
}

type FieldManifest struct {
	Entity string `json:"entity"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Label  string `json:"label,omitempty"`
}

func DecodeManifest(r io.Reader) (Manifest, error) {
	var manifest Manifest
	if err := json.NewDecoder(r).Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (m Manifest) Validate() error {
	if m.Name == "" {
		return errors.New("plugin manifest requires name")
	}
	if m.Version == "" {
		return errors.New("plugin manifest requires version")
	}
	if m.APIVersion != APIVersion {
		return fmt.Errorf("unsupported plugin apiVersion %q", m.APIVersion)
	}
	if m.Entry == "" {
		return errors.New("plugin manifest requires entry")
	}
	return nil
}
