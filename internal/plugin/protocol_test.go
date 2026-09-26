package plugin

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestDecodeInvocationFixture(t *testing.T) {
	fixture := readProtocolFixture(t, "invocation.item-image-inputs-changed.json")
	invocation, err := DecodeInvocation(bytes.NewReader(fixture))
	if err != nil {
		t.Fatalf("DecodeInvocation returned error: %v", err)
	}
	if invocation.Event.Name != EventItemImageInputsChanged || invocation.Event.Item.ImageURL != "https://example.com/image.jpg" {
		t.Fatalf("invocation event = %#v", invocation.Event)
	}
	assertProtocolRoundTrip(t, invocation, fixture)
}

func TestDecodeResponseFixtures(t *testing.T) {
	for _, name := range []string{"response.asset-attach.json", "response.item-star.json"} {
		t.Run(name, func(t *testing.T) {
			fixture := readProtocolFixture(t, name)
			response, err := DecodeResponse(bytes.NewReader(fixture))
			if err != nil {
				t.Fatalf("DecodeResponse returned error: %v", err)
			}
			assertProtocolRoundTrip(t, response, fixture)
		})
	}
}

func TestDecodeResponseRejectsFeedAssetAttachment(t *testing.T) {
	response := `{"ops":[{"op":"asset.attach","entity":"feed","itemId":"item-id","role":"thumbnail","url":"https://example.com/image.jpg"}]}`
	if _, err := DecodeResponse(strings.NewReader(response)); err == nil {
		t.Fatal("DecodeResponse accepted a feed asset attachment")
	}
}

func TestDecodeResponseRejectsUnsafeAssetURL(t *testing.T) {
	response := `{"ops":[{"op":"asset.attach","itemId":"item-id","role":"thumbnail","url":"http://127.0.0.1/image.png"}]}`
	if _, err := DecodeResponse(strings.NewReader(response)); err == nil {
		t.Fatal("DecodeResponse accepted an unsafe asset URL")
	}
}

func TestDecodeProtocolJSONRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	unknownField := `{"ops":[],"unexpected":true}`
	if _, err := DecodeResponse(strings.NewReader(unknownField)); err == nil {
		t.Fatal("DecodeResponse accepted an unknown field")
	}
	trailingValue := `{"ops":[]} {"ops":[]}`
	if _, err := DecodeResponse(strings.NewReader(trailingValue)); err == nil {
		t.Fatal("DecodeResponse accepted trailing JSON")
	}
}

func TestDecodeResponseRequiresOperations(t *testing.T) {
	for _, input := range []string{`{}`, `{"ops":null}`} {
		if _, err := DecodeResponse(strings.NewReader(input)); err == nil {
			t.Errorf("DecodeResponse(%s) succeeded, want missing ops error", input)
		}
	}
}

func readProtocolFixture(t *testing.T, name string) []byte {
	t.Helper()
	fixture, err := os.ReadFile("testdata/v1/" + name)
	if err != nil {
		t.Fatalf("ReadFile(%q) returned error: %v", name, err)
	}
	return fixture
}

func assertProtocolRoundTrip(t *testing.T, value any, fixture []byte) {
	t.Helper()
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent returned error: %v", err)
	}
	if !bytes.Equal(encoded, bytes.TrimSpace(fixture)) {
		t.Fatalf("round-trip JSON = %s, want %s", encoded, bytes.TrimSpace(fixture))
	}
}
