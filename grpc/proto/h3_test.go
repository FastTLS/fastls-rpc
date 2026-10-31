package proto

import (
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestH3IsAnAdditiveRequestField(t *testing.T) {
	request := &FetchRequest{Url: "https://example.test", HttpVersion: "h3"}
	field := request.ProtoReflect().Descriptor().Fields().ByName("http_version")
	if field == nil || field.Number() != 16 {
		t.Fatal("protocol schema changed")
	}
	old := &FetchRequest{Url: request.Url}
	wire, err := proto.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	var decoded FetchRequest
	if err = proto.Unmarshal(wire, &decoded); err != nil || decoded.HttpVersion != "" || decoded.Url != old.Url {
		t.Fatal("legacy request changed")
	}
	if err = protojson.Unmarshal([]byte(`{"url":"https://example.test","http_version":"h3"}`), &decoded); err != nil || decoded.HttpVersion != "h3" {
		t.Fatal("JSON protocol selector mismatch", err)
	}
	if err = protojson.Unmarshal([]byte(`{"http_version":3}`), &decoded); err == nil {
		t.Fatal("invalid selector type accepted")
	}
}
