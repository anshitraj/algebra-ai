package webdiscovery

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/project-algebra/algebra/internal/app"
)

type grounder struct {
	text   string
	err    error
	prompt string
}

func (g *grounder) GroundedText(_ context.Context, p string) (string, error) {
	g.prompt = p
	return g.text, g.err
}

func TestFindParsesWhatLooksLikeAnEndpointAndNothingElse(t *testing.T) {
	g := &grounder{text: "Here you go:\n```json\n" + `[
	  {"name":"Token Doctor","url":"https://api.tokendoctor.example/v1/check","method":"post","description":"rug check"},
	  {"name":"Dupe","url":"https://api.tokendoctor.example/v1/check","method":"POST","description":"same again"},
	  {"name":"Plain http","url":"http://insecure.example/x","method":"GET"},
	  {"name":"Creds","url":"https://user:pw@creds.example/x","method":"GET"},
	  {"name":"Bare IP","url":"https://127.0.0.1/x","method":"GET"},
	  {"name":"Single label","url":"https://localhost/x","method":"GET"},
	  {"name":"A redirect, not an endpoint","url":"https://vertexaisearch.cloud.google.com/grounding-api-redirect/abc","method":"GET"},
	  {"name":"Fragment","url":"https://frag.example/x#y","method":"GET"},
	  {"name":"","url":"https://nameless.example/price?x=1","method":"DELETE"},
	  {"name":"Not a url","url":"::::","method":"GET"}
	]` + "\n```\nHope that helps."}
	got, err := New(g).Find(context.Background(), "token risk", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("two endpoints survive: %+v", got)
	}
	if got[0] != (app.WebFound{Name: "Token Doctor", URL: "https://api.tokendoctor.example/v1/check", Method: "POST", Description: "rug check"}) {
		t.Errorf("first: %+v", got[0])
	}
	// An unknown method becomes GET and a missing name becomes the host.
	if got[1].Method != "GET" || got[1].Name != "nameless.example" || got[1].URL != "https://nameless.example/price?x=1" {
		t.Errorf("second: %+v", got[1])
	}
	if !strings.Contains(g.prompt, "token risk") || !strings.Contains(g.prompt, "Never guess or construct a URL") {
		t.Errorf("the prompt says what is wanted and forbids invented URLs: %q", g.prompt)
	}
}

func TestFindBoundsWhatItReturns(t *testing.T) {
	var b strings.Builder
	b.WriteString("[")
	for i := range 30 {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"name":"n","url":"https://host` + string(rune('a'+i%26)) + string(rune('a'+i/26)) + `.example/x","method":"GET"}`)
	}
	b.WriteString("]")
	got, err := New(&grounder{text: b.String()}).Find(context.Background(), "x", 100)
	if err != nil || len(got) != app.MaxWebFinds {
		t.Fatalf("at most %d, whatever the model over-delivers or the caller asks: %d %v", app.MaxWebFinds, len(got), err)
	}
	if got, _ := New(&grounder{text: b.String()}).Find(context.Background(), "x", 3); len(got) != 3 {
		t.Errorf("a smaller limit holds: %d", len(got))
	}
}

func TestFindGivesBackNothingForAnswersItCantRead(t *testing.T) {
	for _, text := range []string{"", "I couldn't find any.", "[not json]", `{"url":"https://a.example/x"}`, "[1,2,3]"} {
		got, err := New(&grounder{text: text}).Find(context.Background(), "x", 5)
		if err != nil || len(got) != 0 {
			t.Errorf("%q: %v %v", text, got, err)
		}
	}
}

func TestFindPassesTheModelsFailureOn(t *testing.T) {
	boom := errors.New("Gemini rejected the API key")
	if _, err := New(&grounder{err: boom}).Find(context.Background(), "x", 5); !errors.Is(err, boom) {
		t.Errorf("a failed search is an error, not an empty answer: %v", err)
	}
}
