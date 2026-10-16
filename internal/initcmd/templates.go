package initcmd

import (
	"embed"
	"encoding/json"
)

// templatesFS holds every template directory verbatim. "all:" keeps dot
// files such as .env.example.
//
//go:embed all:templates
var templatesFS embed.FS

// templateOrder is the order init shows templates in.
var templateOrder = []string{"blank", "simple-loop", "sequential-reviewer", "parallel-planner", "parallel-planner-with-review"}

// ListTemplates returns the embedded templates in display order.
func ListTemplates() []TemplateEntry {
	out := make([]TemplateEntry, 0, len(templateOrder))
	for _, name := range templateOrder {
		b, err := templatesFS.ReadFile("templates/" + name + "/template.json")
		if err != nil {
			panic("template.json missing for " + name + ": " + err.Error())
		}
		var e TemplateEntry
		if err := json.Unmarshal(b, &e); err != nil {
			panic("template.json invalid for " + name + ": " + err.Error())
		}
		out = append(out, e)
	}
	return out
}

// GetTemplate returns the template with the given name.
func GetTemplate(name string) (TemplateEntry, bool) {
	for _, t := range ListTemplates() {
		if t.Name == name {
			return t, true
		}
	}
	return TemplateEntry{}, false
}
