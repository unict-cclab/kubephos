package externaldestination

import "testing"

func TestEndpoints(t *testing.T) {
	legacy, err := Normalize(" https://one.example/v1 ", nil)
	if err != nil || len(legacy) != 1 || legacy[0].Label != "Endpoint 1" || legacy[0].URL != "https://one.example/v1" {
		t.Fatalf("legacy endpoint: %#v %v", legacy, err)
	}
	endpoints, err := Normalize("", []Endpoint{{Label: " West ", URL: "https://west.example"}, {URL: "http://east.example/v2"}})
	if err != nil || endpoints[0].Label != "West" || endpoints[1].Label != "Endpoint 2" {
		t.Fatalf("endpoints: %#v %v", endpoints, err)
	}
	for _, values := range [][]Endpoint{
		{{Label: "West", URL: "http://one.example"}, {Label: " west ", URL: "http://two.example"}},
		{{Label: "Endpoint 2", URL: "http://one.example"}, {URL: "http://two.example"}},
		{{URL: "https://user:secret@example.test"}}, {{URL: "file:///tmp/test"}}, {{URL: "https://example.test/#fragment"}}, {{URL: ""}},
	} {
		if _, err := Normalize("", values); err == nil {
			t.Fatalf("invalid endpoints accepted: %#v", values)
		}
	}
}
