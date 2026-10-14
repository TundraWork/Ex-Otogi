package otogi

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The corpus holds one event per protocol the model was designed against. Each
// must decode into Event, validate, and survive a JSON round trip unchanged.
func TestCrossProtocolCorpus(t *testing.T) {
	files, err := filepath.Glob("testdata/*.json")
	if err != nil || len(files) != 6 {
		t.Fatalf("corpus files = %v, %v", files, err)
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var event Event
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&event); err != nil {
				t.Fatal(err)
			}
			if err := event.Validate(); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			var original, roundTrip any
			_ = json.Unmarshal(data, &original)
			_ = json.Unmarshal(encoded, &roundTrip)
			a, _ := json.Marshal(original)
			b, _ := json.Marshal(roundTrip)
			if !bytes.Equal(a, b) {
				t.Fatalf("round trip changed event:\n got %s\nwant %s", b, a)
			}
		})
	}
}
