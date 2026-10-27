package assemble

import (
	"encoding/json"
	"testing"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
)

func TestBuildFormRemovesPIIAndKeepsTheOrder(t *testing.T) {
	desc, send := "We reply", "Sending"
	schema := `{"type":"object","required":["email","topic"],"properties":{
	  "topic":{"type":"string","enum":["a","b"]},
	  "email":{"type":"string","format":"email","x-pii":true},
	  "ghost":{"type":"string"}}}`
	f := entities.PublishedForm{Slug: "c", ResolvedLocale: "en", Title: "T", Description: &desc, SubmitLabel: "Go", SendingLabel: &send,
		JSONSchema: entities.JSONB(schema), FieldOrder: entities.StringArray{"email", "topic", "missing"},
		Messages: entities.JSONB(`{"required":"Req"}`)}
	fields := []entities.PublishedFormField{
		{FieldKey: "email", Label: "Email"},
		{FieldKey: "topic", Label: "Topic", OptionLabels: entities.JSONB(`{"a":"Option A"}`)},
	}
	def, err := BuildForm(f, fields)
	if err != nil {
		t.Fatal(err)
	}
	if len(def.Fields) != 3 || def.Fields[0].Key != "email" || def.Fields[1].Key != "topic" {
		t.Fatalf("fields: %+v", def.Fields)
	}
	if !def.Fields[0].Required || !def.Fields[1].Required {
		t.Error("required comes from the schema")
	}
	if sb, _ := json.Marshal(def.Fields[0].Schema); string(sb) != `{"format":"email","type":"string"}` {
		t.Errorf("x-pii must be removed: %s", sb)
	}
	// a property that field_order forgets still comes (alphabetically, after the ordered ones)
	if def.Fields[2].Key != "ghost" {
		t.Errorf("unordered property: %+v", def.Fields)
	}
	opts := def.Fields[1].Options
	if len(opts) != 2 || opts[0].Label != "Option A" || opts[1].Label != "b" {
		t.Errorf("options: %+v", opts)
	}
	b, _ := json.Marshal(def)
	var back map[string]any
	_ = json.Unmarshal(b, &back)
	if back["honeypot_field"] != "" || back["success_redirect_url"] != nil || back["messages"].(map[string]any)["required"] != "Req" {
		t.Errorf("form: %s", b)
	}
	if _, leaked := back["rate_limit_max"]; leaked {
		t.Error("rate limits must not be exposed")
	}
}

func TestBuildFAQItemsSanitisesAnswers(t *testing.T) {
	rows := []entities.PublishedFAQItem{{FAQItemID: 7, Question: "Q?",
		Answer: entities.JSONB(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"A","marks":[{"type":"underline"}]}]}]}`)}}
	items := BuildFAQItems(rows, nil)
	b, _ := json.Marshal(items)
	if string(b) != `[{"id":7,"question":"Q?","answer":{"content":[{"content":[{"text":"A","type":"text"}],"type":"paragraph"}],"type":"doc"}}]` {
		t.Errorf("items: %s", b)
	}
}
