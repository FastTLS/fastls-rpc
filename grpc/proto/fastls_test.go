package proto

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestFetchRequestDescriptorAndRoundTrip(t *testing.T) {
	request := &FetchRequest{
		Url:                 "https://example.com/",
		Method:              "GET",
		Browser:             "chrome",
		Http2SettingsString: "1:65536|15663105|0|m,a,s,p",
		ParentProxy:         "http://127.0.0.1:8080",
	}

	descriptor := request.ProtoReflect().Descriptor()
	for name, number := range map[string]int32{
		"url":                   1,
		"http2_settings_string": 12,
		"parent_proxy":          13,
	} {
		field := descriptor.Fields().ByName(protoreflect.Name(name))
		if field == nil {
			t.Fatalf("field %q is missing from the generated descriptor", name)
		}
		if got := int32(field.Number()); got != number {
			t.Fatalf("field %q number = %d, want %d", name, got, number)
		}
	}

	encoded, err := proto.Marshal(request)
	if err != nil {
		t.Fatalf("marshal FetchRequest: %v", err)
	}

	var decoded FetchRequest
	if err := proto.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal FetchRequest: %v", err)
	}
	if !proto.Equal(request, &decoded) {
		t.Fatalf("round trip mismatch: got %v, want %v", &decoded, request)
	}
}
